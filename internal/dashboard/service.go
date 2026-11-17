package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Repository is the persistence the service needs (implemented in repository.go).
type Repository interface {
	// WithTenant runs fn in one transaction scoped to tenantID (RLS).
	WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(Store) error) error
}

// Store is the set of queries available inside a tenant transaction.
type Store interface {
	Pipeline(ctx context.Context) ([]StageSummary, error)
	// Monthly returns the 6 months ending with the month of asOf, oldest first.
	Monthly(ctx context.Context, asOf time.Time) ([]MonthSummary, error)
}

// Service implements the dashboard use cases.
type Service struct {
	repo Repository
	now  func() time.Time
}

// NewService wires the dashboard service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// Pipeline returns every stage in pipeline order, with zeros for empty stages.
func (s *Service) Pipeline(ctx context.Context, tenantID uuid.UUID) ([]StageSummary, error) {
	var out []StageSummary
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		var err error
		out, err = st.Pipeline(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline summary: %w", err)
	}
	return out, nil
}

// Monthly returns the last 6 months up to and including the current one.
func (s *Service) Monthly(ctx context.Context, tenantID uuid.UUID) ([]MonthSummary, error) {
	var out []MonthSummary
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		var err error
		out, err = st.Monthly(ctx, s.now())
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("monthly sales: %w", err)
	}
	return out, nil
}
