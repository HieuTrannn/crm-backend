package contact

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Repository is the persistence the service needs (implemented in repository.go).
type Repository interface {
	// WithTenant runs fn in one transaction scoped to tenantID (RLS). fn's error rolls it back.
	WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(Store) error) error
}

// Store is the set of queries available inside a tenant transaction.
// Missing (or soft-deleted, or other-tenant) contacts return ErrNotFound.
type Store interface {
	Create(ctx context.Context, id uuid.UUID, in CreateInput) (Contact, error)
	Get(ctx context.Context, id uuid.UUID) (Contact, error)
	// GetForUpdate locks the row until the transaction ends.
	GetForUpdate(ctx context.Context, id uuid.UUID) (Contact, error)
	Update(ctx context.Context, c Contact) (Contact, error)
	SoftDelete(ctx context.Context, id uuid.UUID) error
	// List returns up to p.Limit rows after the cursor; p.Query is LIKE-escaped.
	List(ctx context.Context, p ListParams) ([]Contact, error)
}

// DealChecker reports whether a contact still has open deals (FR-CON-04).
type DealChecker interface {
	HasOpenDeals(ctx context.Context, tenantID, contactID uuid.UUID) (bool, error)
}

// NoDeals is the DealChecker used until the deal module exists.
// TODO(deal): replace with a query on deals that runs in the same transaction as
// the soft delete (and locks the contact), so a deal created concurrently cannot slip in.
type NoDeals struct{}

// HasOpenDeals implements DealChecker.
func (NoDeals) HasOpenDeals(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return false, nil }

// Service implements the contact use cases. Every method is scoped to tenantID.
type Service struct {
	repo  Repository
	deals DealChecker
}

// NewService wires the contact service.
func NewService(repo Repository, deals DealChecker) *Service {
	return &Service{repo: repo, deals: deals}
}

// Create adds a contact; the owner defaults to the caller (FR-CON-01).
func (s *Service) Create(ctx context.Context, tenantID, callerID uuid.UUID, in CreateInput) (Contact, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Contact{}, fmt.Errorf("new contact id: %w", err)
	}
	if in.OwnerID == nil {
		in.OwnerID = &callerID
	}
	in.Email, in.Phone, in.Company, in.Notes = blankToNil(in.Email), blankToNil(in.Phone), blankToNil(in.Company), blankToNil(in.Notes)

	var c Contact
	err = s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		var err error
		c, err = st.Create(ctx, id, in)
		return err
	})
	if err != nil {
		return Contact{}, fmt.Errorf("create contact: %w", err)
	}
	return c, nil
}

// Get returns one live contact.
func (s *Service) Get(ctx context.Context, tenantID, id uuid.UUID) (Contact, error) {
	var c Contact
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		var err error
		c, err = st.Get(ctx, id)
		return err
	})
	if err != nil {
		return Contact{}, fmt.Errorf("get contact: %w", err)
	}
	return c, nil
}

// Update applies a partial update. The row is locked while it is read and
// written back, so two concurrent PATCHes cannot overwrite each other's fields.
func (s *Service) Update(ctx context.Context, tenantID, id uuid.UUID, in UpdateInput) (Contact, error) {
	var c Contact
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		cur, err := st.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if in.FullName != nil {
			cur.FullName = *in.FullName
		}
		if in.Email != nil {
			cur.Email = blankToNil(in.Email)
		}
		if in.Phone != nil {
			cur.Phone = blankToNil(in.Phone)
		}
		if in.Company != nil {
			cur.Company = blankToNil(in.Company)
		}
		if in.Notes != nil {
			cur.Notes = blankToNil(in.Notes)
		}
		if in.OwnerID != nil {
			cur.OwnerID = *in.OwnerID
		}
		c, err = st.Update(ctx, cur)
		return err
	})
	if err != nil {
		return Contact{}, fmt.Errorf("update contact: %w", err)
	}
	return c, nil
}

// Delete soft-deletes a contact unless it still has open deals (FR-CON-04).
func (s *Service) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		if _, err := st.GetForUpdate(ctx, id); err != nil {
			return err
		}
		open, err := s.deals.HasOpenDeals(ctx, tenantID, id)
		if err != nil {
			return fmt.Errorf("check open deals: %w", err)
		}
		if open {
			return ErrHasOpenDeals
		}
		return st.SoftDelete(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	return nil
}

// List returns one page, newest first, and whether more rows follow (FR-CON-02/03).
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, p ListParams) ([]Contact, bool, error) {
	p.Query = escapeLike(strings.TrimSpace(p.Query))
	want := p.Limit
	p.Limit++ // one extra row tells whether a next page exists

	var rows []Contact
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		var err error
		rows, err = st.List(ctx, p)
		return err
	})
	if err != nil {
		return nil, false, fmt.Errorf("list contacts: %w", err)
	}
	if len(rows) > want {
		return rows[:want], true, nil
	}
	return rows, false, nil
}

// escapeLike makes % and _ in user input match literally in a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// blankToNil stores "" (or whitespace) as NULL, so "no email" has one representation.
func blankToNil(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	return s
}
