package deal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/HieuTrannn/crm-backend/db/sqlc"
	"github.com/HieuTrannn/crm-backend/internal/platform/database"
)

// PostgresRepository implements Repository on top of sqlc.
type PostgresRepository struct {
	db *database.DB
}

// NewPostgresRepository creates the repository.
func NewPostgresRepository(db *database.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// WithTenant implements Repository.
func (r *PostgresRepository) WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(Store) error) error {
	return r.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return fn(&store{q: sqlc.New(tx), tenantID: tenantID})
	})
}

// store runs sqlc queries inside one tenant transaction. Every query also
// filters by tenantID explicitly (layer 1); RLS is layer 2.
type store struct {
	q        *sqlc.Queries
	tenantID uuid.UUID
}

func (s *store) LockLiveContact(ctx context.Context, contactID uuid.UUID) error {
	_, err := s.q.LockLiveContact(ctx, sqlc.LockLiveContactParams{ID: contactID, TenantID: s.tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidContact
	}
	if err != nil {
		return fmt.Errorf("lock contact: %w", err)
	}
	return nil
}

func (s *store) Create(ctx context.Context, id uuid.UUID, in CreateInput) (Deal, error) {
	row, err := s.q.CreateDeal(ctx, sqlc.CreateDealParams{
		ID: id, TenantID: s.tenantID, ContactID: in.ContactID, Title: in.Title, Amount: in.Amount,
		ExpectedCloseDate: dateToTime(in.ExpectedCloseDate), OwnerID: *in.OwnerID,
	})
	if err != nil {
		return Deal{}, mapWriteError(err)
	}
	return toDeal(row), nil
}

func (s *store) Get(ctx context.Context, id uuid.UUID) (Deal, error) {
	row, err := s.q.GetDeal(ctx, sqlc.GetDealParams{ID: id, TenantID: s.tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Deal{}, ErrNotFound
	}
	if err != nil {
		return Deal{}, fmt.Errorf("get deal: %w", err)
	}
	return toDeal(row), nil
}

func (s *store) Update(ctx context.Context, d Deal, version int32) (Deal, error) {
	row, err := s.q.UpdateDeal(ctx, sqlc.UpdateDealParams{
		ID: d.ID, TenantID: s.tenantID, Version: version, Title: d.Title, Amount: d.Amount,
		ExpectedCloseDate: dateToTime(d.ExpectedCloseDate), OwnerID: d.OwnerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Deal{}, errStale
	}
	if err != nil {
		return Deal{}, mapWriteError(err)
	}
	return toDeal(row), nil
}

func (s *store) SetStage(ctx context.Context, id uuid.UUID, version int32, to Stage, lostReason *string) (Deal, error) {
	row, err := s.q.TransitionDeal(ctx, sqlc.TransitionDealParams{
		ID: id, TenantID: s.tenantID, Version: version, Stage: string(to), LostReason: lostReason,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Deal{}, errStale
	}
	if err != nil {
		return Deal{}, fmt.Errorf("transition deal: %w", err)
	}
	return toDeal(row), nil
}

func (s *store) SoftDelete(ctx context.Context, id uuid.UUID, version int32) error {
	n, err := s.q.SoftDeleteDeal(ctx, sqlc.SoftDeleteDealParams{ID: id, TenantID: s.tenantID, Version: version})
	if err != nil {
		return fmt.Errorf("soft delete deal: %w", err)
	}
	if n == 0 {
		return errStale
	}
	return nil
}

func (s *store) AddStageChange(ctx context.Context, c stageChange) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("new history id: %w", err)
	}
	var from *string
	if c.From != nil {
		f := string(*c.From)
		from = &f
	}
	err = s.q.InsertStageHistory(ctx, sqlc.InsertStageHistoryParams{
		ID: id, TenantID: s.tenantID, DealID: c.DealID, FromStage: from, ToStage: string(c.To),
		Reason: c.Reason, ChangedBy: c.ChangedBy,
	})
	if err != nil {
		return fmt.Errorf("insert stage history: %w", err)
	}
	return nil
}

func (s *store) History(ctx context.Context, dealID uuid.UUID) ([]HistoryEntry, error) {
	rows, err := s.q.ListStageHistory(ctx, sqlc.ListStageHistoryParams{TenantID: s.tenantID, DealID: dealID})
	if err != nil {
		return nil, fmt.Errorf("list stage history: %w", err)
	}
	out := make([]HistoryEntry, len(rows))
	for i, r := range rows {
		out[i] = HistoryEntry{ID: r.ID, ToStage: Stage(r.ToStage), Reason: r.Reason, ChangedBy: r.ChangedBy, ChangedAt: r.ChangedAt}
		if r.FromStage != nil {
			from := Stage(*r.FromStage)
			out[i].FromStage = &from
		}
	}
	return out, nil
}

func (s *store) List(ctx context.Context, p ListParams) ([]Deal, error) {
	params := sqlc.ListDealsParams{
		TenantID: s.tenantID, AfterCreatedAt: p.AfterCreatedAt, AfterID: p.AfterID,
		RowLimit: int32(p.Limit), //nolint:gosec // limit is capped at 101
	}
	if p.Stage != nil {
		stage := string(*p.Stage)
		params.Stage = &stage
	}
	if p.OwnerID != nil {
		params.OwnerID = uuid.NullUUID{UUID: *p.OwnerID, Valid: true}
	}
	if p.ContactID != nil {
		params.ContactID = uuid.NullUUID{UUID: *p.ContactID, Valid: true}
	}
	rows, err := s.q.ListDeals(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list deals: %w", err)
	}
	out := make([]Deal, len(rows))
	for i, r := range rows {
		out[i] = toDeal(r)
	}
	return out, nil
}

func toDeal(r sqlc.Deal) Deal {
	var closeDate *Date
	if r.ExpectedCloseDate != nil {
		closeDate = &Date{Time: *r.ExpectedCloseDate}
	}
	return Deal{
		ID: r.ID, ContactID: r.ContactID, Title: r.Title, Amount: r.Amount, Stage: Stage(r.Stage),
		ExpectedCloseDate: closeDate, OwnerID: r.OwnerID, LostReason: r.LostReason, Version: r.Version,
		ClosedAt: r.ClosedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func dateToTime(d *Date) *time.Time {
	if d == nil {
		return nil
	}
	return &d.Time
}

// mapWriteError turns constraint violations into business errors.
func mapWriteError(err error) error {
	switch {
	case database.ViolatesConstraint(err, "deals_contact_fkey"):
		return ErrInvalidContact
	case database.ViolatesConstraint(err, "deals_owner_fkey"):
		return ErrInvalidOwner
	default:
		return fmt.Errorf("write deal: %w", err)
	}
}
