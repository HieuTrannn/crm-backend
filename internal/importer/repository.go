package importer

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

func (s *store) CreateJob(ctx context.Context, id, createdBy uuid.UUID) (Job, error) {
	row, err := s.q.CreateImportJob(ctx, sqlc.CreateImportJobParams{ID: id, TenantID: s.tenantID, CreatedBy: createdBy})
	if err != nil {
		return Job{}, fmt.Errorf("insert import job: %w", err)
	}
	return toJob(row), nil
}

func (s *store) GetJob(ctx context.Context, id uuid.UUID) (Job, error) {
	row, err := s.q.GetImportJob(ctx, sqlc.GetImportJobParams{ID: id, TenantID: s.tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get import job: %w", err)
	}
	return toJob(row), nil
}

func (s *store) StartJob(ctx context.Context, id uuid.UUID, totalRows int32) error {
	return s.q.StartImportJob(ctx, sqlc.StartImportJobParams{ID: id, TenantID: s.tenantID, TotalRows: totalRows})
}

func (s *store) FinishJob(ctx context.Context, id uuid.UUID, status string, reason *string) error {
	return s.q.FinishImportJob(ctx, sqlc.FinishImportJobParams{
		ID: id, TenantID: s.tenantID, Status: status, FailureReason: reason,
	})
}

func (s *store) InsertContacts(ctx context.Context, ownerID uuid.UUID, rows []contactRow) ([]uuid.UUID, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	p := sqlc.InsertImportedContactsParams{
		TenantID:  s.tenantID,
		OwnerID:   ownerID,
		Ids:       make([]uuid.UUID, len(rows)),
		FullNames: make([]string, len(rows)),
		Emails:    make([]string, len(rows)),
		Phones:    make([]string, len(rows)),
		Companies: make([]string, len(rows)),
		Notes:     make([]string, len(rows)),
	}
	for i, r := range rows {
		p.Ids[i], p.FullNames[i], p.Emails[i] = r.id, r.fullName, r.email
		p.Phones[i], p.Companies[i], p.Notes[i] = r.phone, r.company, r.notes
	}
	ids, err := s.q.InsertImportedContacts(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("insert contacts: %w", err)
	}
	return ids, nil
}

func (s *store) InsertErrors(ctx context.Context, jobID uuid.UUID, errs []RowError) error {
	if len(errs) == 0 {
		return nil
	}
	p := sqlc.InsertImportErrorsParams{
		JobID: jobID, TenantID: s.tenantID,
		RowNumbers: make([]int32, len(errs)), Reasons: make([]string, len(errs)),
	}
	for i, e := range errs {
		p.RowNumbers[i], p.Reasons[i] = e.RowNumber, e.Reason
	}
	if err := s.q.InsertImportErrors(ctx, p); err != nil {
		return fmt.Errorf("insert import errors: %w", err)
	}
	return nil
}

func (s *store) AddProgress(ctx context.Context, jobID uuid.UUID, processed, succeeded, failed int32) error {
	return s.q.AddImportProgress(ctx, sqlc.AddImportProgressParams{
		ID: jobID, TenantID: s.tenantID, Processed: processed, Succeeded: succeeded, Failed: failed,
	})
}

func (s *store) ListErrors(ctx context.Context, jobID uuid.UUID, afterRow int32, limit int) ([]RowError, error) {
	rows, err := s.q.ListImportErrors(ctx, sqlc.ListImportErrorsParams{
		JobID: jobID, TenantID: s.tenantID, AfterRow: afterRow,
		RowLimit: int32(limit), //nolint:gosec // limit is capped at 101
	})
	if err != nil {
		return nil, fmt.Errorf("list import errors: %w", err)
	}
	out := make([]RowError, len(rows))
	for i, r := range rows {
		out[i] = RowError{RowNumber: r.RowNumber, Reason: r.Reason}
	}
	return out, nil
}

func toJob(r sqlc.ImportJob) Job {
	return Job{
		ID: r.ID, Status: r.Status, TotalRows: r.TotalRows, ProcessedRows: r.ProcessedRows,
		SuccessRows: r.SuccessRows, ErrorRows: r.ErrorRows, FailureReason: r.FailureReason,
		CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt,
	}
}
