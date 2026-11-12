package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoTenant is returned when WithTenantTx is called without a tenant.
var ErrNoTenant = errors.New("tenant id is required")

// DB runs tenant-scoped transactions on a connection pool.
type DB struct {
	pool *pgxpool.Pool
}

// New wraps a pool. The pool's lifetime stays with the caller.
func New(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool}
}

// WithTenantTx runs fn in a transaction where PostgreSQL RLS sees tenantID.
// set_config(..., true) is transaction-local, so the tenant is cleared on
// commit/rollback and never leaks to the next user of the pooled connection.
// fn's error rolls the transaction back; nil commits it.
func (db *DB) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	// RLS would already return nothing for the nil UUID; failing here gives a clearer error.
	if tenantID == uuid.Nil {
		return ErrNoTenant
	}
	return pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
			return fmt.Errorf("set tenant: %w", err)
		}
		return fn(tx)
	})
}
