package contact

import (
	"context"
	"errors"
	"fmt"

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

func (s *store) Create(ctx context.Context, id uuid.UUID, in CreateInput) (Contact, error) {
	row, err := s.q.CreateContact(ctx, sqlc.CreateContactParams{
		ID: id, TenantID: s.tenantID, FullName: in.FullName, Email: in.Email,
		Phone: in.Phone, Company: in.Company, Notes: in.Notes, OwnerID: *in.OwnerID,
	})
	return toContact(row, mapWriteError(err))
}

func (s *store) Get(ctx context.Context, id uuid.UUID) (Contact, error) {
	row, err := s.q.GetContact(ctx, sqlc.GetContactParams{ID: id, TenantID: s.tenantID})
	return toContact(row, err)
}

func (s *store) GetForUpdate(ctx context.Context, id uuid.UUID) (Contact, error) {
	row, err := s.q.GetContactForUpdate(ctx, sqlc.GetContactForUpdateParams{ID: id, TenantID: s.tenantID})
	return toContact(row, err)
}

func (s *store) Update(ctx context.Context, c Contact) (Contact, error) {
	row, err := s.q.UpdateContact(ctx, sqlc.UpdateContactParams{
		ID: c.ID, TenantID: s.tenantID, FullName: c.FullName, Email: c.Email,
		Phone: c.Phone, Company: c.Company, Notes: c.Notes, OwnerID: c.OwnerID,
	})
	return toContact(row, mapWriteError(err))
}

func (s *store) SoftDelete(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.SoftDeleteContact(ctx, sqlc.SoftDeleteContactParams{ID: id, TenantID: s.tenantID})
	if err != nil {
		return fmt.Errorf("soft delete contact: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *store) List(ctx context.Context, p ListParams) ([]Contact, error) {
	var rows []sqlc.Contact
	var err error
	if p.Query == "" {
		rows, err = s.q.ListContacts(ctx, sqlc.ListContactsParams{
			TenantID: s.tenantID, AfterCreatedAt: p.AfterCreatedAt, AfterID: p.AfterID, RowLimit: int32(p.Limit), //nolint:gosec // limit is capped at 101
		})
	} else {
		rows, err = s.q.SearchContacts(ctx, sqlc.SearchContactsParams{
			TenantID: s.tenantID, Query: p.Query, AfterCreatedAt: p.AfterCreatedAt, AfterID: p.AfterID, RowLimit: int32(p.Limit), //nolint:gosec // limit is capped at 101
		})
	}
	if err != nil {
		return nil, fmt.Errorf("list contacts: %w", err)
	}
	out := make([]Contact, len(rows))
	for i, r := range rows {
		out[i], _ = toContact(r, nil)
	}
	return out, nil
}

// toContact maps a sqlc row, turning "no row" into ErrNotFound.
func toContact(r sqlc.Contact, err error) (Contact, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	if err != nil {
		return Contact{}, err
	}
	return Contact{
		ID: r.ID, FullName: r.FullName, Email: r.Email, Phone: r.Phone, Company: r.Company,
		Notes: r.Notes, OwnerID: r.OwnerID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

// mapWriteError turns constraint violations into business errors.
func mapWriteError(err error) error {
	switch {
	case err == nil:
		return nil
	case database.ViolatesConstraint(err, "contacts_tenant_email_key"):
		return ErrEmailTaken
	case database.ViolatesConstraint(err, "contacts_owner_fkey"):
		return ErrInvalidOwner
	default:
		return fmt.Errorf("write contact: %w", err)
	}
}
