package dashboard

import (
	"context"
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

type store struct {
	q        *sqlc.Queries
	tenantID uuid.UUID
}

func (s *store) Pipeline(ctx context.Context) ([]StageSummary, error) {
	rows, err := s.q.PipelineSummary(ctx, s.tenantID)
	if err != nil {
		return nil, fmt.Errorf("query pipeline: %w", err)
	}
	out := make([]StageSummary, len(rows))
	for i, r := range rows {
		out[i] = StageSummary{Stage: r.Stage, DealCount: r.DealCount, TotalAmount: r.TotalAmount}
	}
	return out, nil
}

func (s *store) Monthly(ctx context.Context, asOf time.Time) ([]MonthSummary, error) {
	rows, err := s.q.MonthlySales(ctx, sqlc.MonthlySalesParams{TenantID: s.tenantID, AsOf: asOf})
	if err != nil {
		return nil, fmt.Errorf("query monthly sales: %w", err)
	}
	out := make([]MonthSummary, len(rows))
	for i, r := range rows {
		out[i] = MonthSummary{Month: r.Month, WonCount: r.WonCount, LostCount: r.LostCount,
			WonAmount: r.WonAmount, WinRate: r.WinRate}
	}
	return out, nil
}
