package deal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// errStale means a conditional UPDATE matched no row: the version moved on, or the
// deal does not exist. The service re-reads to tell the two apart.
var errStale = errors.New("no deal matched id and version")

// Repository is the persistence the service needs (implemented in repository.go).
type Repository interface {
	// WithTenant runs fn in one transaction scoped to tenantID (RLS). fn's error rolls it back.
	WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(Store) error) error
}

// Store is the set of queries available inside a tenant transaction.
type Store interface {
	// LockLiveContact locks the contact against soft delete; ErrInvalidContact if missing or deleted.
	LockLiveContact(ctx context.Context, contactID uuid.UUID) error
	Create(ctx context.Context, id uuid.UUID, in CreateInput) (Deal, error)
	// Get returns ErrNotFound for a missing (or other-tenant) deal.
	Get(ctx context.Context, id uuid.UUID) (Deal, error)
	// Update and SetStage write only if the stored version still equals version
	// (bumping it), and return errStale otherwise.
	Update(ctx context.Context, d Deal, version int32) (Deal, error)
	SetStage(ctx context.Context, id uuid.UUID, version int32, to Stage, lostReason *string) (Deal, error)
	SoftDelete(ctx context.Context, id uuid.UUID, version int32) error
	AddStageChange(ctx context.Context, c stageChange) error
	// History returns the deal's stage changes, oldest first.
	History(ctx context.Context, dealID uuid.UUID) ([]HistoryEntry, error)
	List(ctx context.Context, p ListParams) ([]Deal, error)
}

// Service implements the deal use cases. Every method is scoped to tenantID.
type Service struct {
	repo Repository
}

// NewService wires the deal service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Create adds a deal in StageLead and records it in the stage history (FR-DEAL-01/03).
func (s *Service) Create(ctx context.Context, tenantID, callerID uuid.UUID, in CreateInput) (Deal, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Deal{}, fmt.Errorf("new deal id: %w", err)
	}
	if in.OwnerID == nil {
		in.OwnerID = &callerID
	}

	var d Deal
	err = s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		if err := st.LockLiveContact(ctx, in.ContactID); err != nil {
			return err
		}
		var err error
		if d, err = st.Create(ctx, id, in); err != nil {
			return err
		}
		return st.AddStageChange(ctx, stageChange{DealID: d.ID, To: StageLead, ChangedBy: callerID})
	})
	if err != nil {
		return Deal{}, fmt.Errorf("create deal: %w", err)
	}
	return d, nil
}

// Get returns one deal.
func (s *Service) Get(ctx context.Context, tenantID, id uuid.UUID) (Deal, error) {
	var d Deal
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		var err error
		d, err = st.Get(ctx, id)
		return err
	})
	if err != nil {
		return Deal{}, fmt.Errorf("get deal: %w", err)
	}
	return d, nil
}

// Update applies a partial update if in.Version is still current (FR-DEAL-04).
// A stale version returns a *VersionConflictError holding the latest deal.
func (s *Service) Update(ctx context.Context, tenantID, id uuid.UUID, in UpdateInput) (Deal, error) {
	var d Deal
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		cur, err := st.Get(ctx, id)
		if err != nil {
			return err
		}
		if cur.Version != in.Version {
			return &VersionConflictError{Latest: cur}
		}
		if in.Title != nil {
			cur.Title = *in.Title
		}
		if in.Amount != nil {
			cur.Amount = *in.Amount
		}
		if in.ExpectedCloseDate != nil {
			cur.ExpectedCloseDate = in.ExpectedCloseDate
		}
		if in.OwnerID != nil {
			cur.OwnerID = *in.OwnerID
		}
		// The version check above can pass for two requests at once; the
		// conditional UPDATE lets exactly one of them win.
		d, err = st.Update(ctx, cur, in.Version)
		if errors.Is(err, errStale) {
			return conflict(ctx, st, id)
		}
		return err
	})
	if err != nil {
		return Deal{}, fmt.Errorf("update deal: %w", err)
	}
	return d, nil
}

// Transition moves a deal along the pipeline and records the change in the same
// transaction (FR-DEAL-02/03/04).
func (s *Service) Transition(ctx context.Context, tenantID, callerID, id uuid.UUID, in TransitionInput) (Deal, error) {
	var d Deal
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		cur, err := st.Get(ctx, id)
		if err != nil {
			return err
		}
		// Check the version first: with a stale version the client's idea of the
		// current stage is stale too, so it should see the latest deal.
		if cur.Version != in.Version {
			return &VersionConflictError{Latest: cur}
		}
		if !CanTransition(cur.Stage, in.To) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, cur.Stage, in.To)
		}

		var lostReason, reason *string
		switch {
		case in.To == StageLost:
			if lostReason = nonBlank(in.LostReason); lostReason == nil {
				return ErrReasonRequired
			}
			reason = lostReason
		case cur.Stage == StageLost: // reopening; lost_reason is cleared
			if reason = nonBlank(in.ReopenReason); reason == nil {
				return ErrReasonRequired
			}
		}

		d, err = st.SetStage(ctx, id, in.Version, in.To, lostReason)
		if errors.Is(err, errStale) {
			return conflict(ctx, st, id)
		}
		if err != nil {
			return err
		}
		from := cur.Stage
		return st.AddStageChange(ctx, stageChange{DealID: id, From: &from, To: in.To, Reason: reason, ChangedBy: callerID})
	})
	if err != nil {
		return Deal{}, fmt.Errorf("transition deal: %w", err)
	}
	return d, nil
}

// Delete soft-deletes a deal if version is still current (FR-DEAL-06). The deal
// then disappears from every read and no longer counts as open for its contact;
// its stage history is kept.
func (s *Service) Delete(ctx context.Context, tenantID, id uuid.UUID, version int32) error {
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		cur, err := st.Get(ctx, id)
		if err != nil {
			return err
		}
		if cur.Version != version {
			return &VersionConflictError{Latest: cur}
		}
		err = st.SoftDelete(ctx, id, version)
		if errors.Is(err, errStale) {
			return conflict(ctx, st, id)
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("delete deal: %w", err)
	}
	return nil
}

// History returns every stage change of a deal, oldest first (FR-DEAL-03).
// A missing or other-tenant deal is ErrNotFound, not an empty history.
func (s *Service) History(ctx context.Context, tenantID, id uuid.UUID) ([]HistoryEntry, error) {
	var entries []HistoryEntry
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		if _, err := st.Get(ctx, id); err != nil {
			return err
		}
		var err error
		entries, err = st.History(ctx, id)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("deal history: %w", err)
	}
	return entries, nil
}

// List returns one page, newest first, and whether more rows follow (FR-DEAL-05).
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, p ListParams) ([]Deal, bool, error) {
	want := p.Limit
	p.Limit++ // one extra row tells whether a next page exists

	var rows []Deal
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		var err error
		rows, err = st.List(ctx, p)
		return err
	})
	if err != nil {
		return nil, false, fmt.Errorf("list deals: %w", err)
	}
	if len(rows) > want {
		return rows[:want], true, nil
	}
	return rows, false, nil
}

// conflict re-reads the deal after a conditional UPDATE matched nothing:
// gone means 404, otherwise someone else changed it first (409 with the latest).
func conflict(ctx context.Context, st Store, id uuid.UUID) error {
	latest, err := st.Get(ctx, id)
	if err != nil {
		return err
	}
	return &VersionConflictError{Latest: latest}
}

func nonBlank(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
