//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx" for goose
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/HieuTrannn/crm-backend/internal/platform/database"
)

// Credentials of a throwaway test container, not real secrets.
const (
	appUserPassword  = "app_user_test_pw" //nolint:gosec // test container only
	migratorPassword = "migrator_test_pw" //nolint:gosec // test container only
)

type env struct {
	db       *database.DB  // app_user, tenant-scoped helper
	app      *pgxpool.Pool // app_user, single connection to prove no tenant leaks between uses
	migrator *pgxpool.Pool // table owner, NOT superuser (like Neon's owner role)
}

// setup starts Postgres, runs the migration up/down/up as a non-superuser owner,
// and connects as app_user, mirroring production roles.
func setup(t *testing.T) env {
	t.Helper()
	ctx := t.Context()

	ctr, err := postgres.Run(ctx, "postgres:17",
		postgres.WithDatabase("crm"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	superURL, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	super, err := pgx.Connect(ctx, superURL)
	require.NoError(t, err)
	for _, stmt := range []string{
		"CREATE ROLE migrator LOGIN CREATEROLE PASSWORD '" + migratorPassword + "'",
		"ALTER DATABASE crm OWNER TO migrator",
		"ALTER SCHEMA public OWNER TO migrator",
	} {
		_, err := super.Exec(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	require.NoError(t, super.Close(ctx))

	migratorURL := withUser(t, superURL, "migrator", migratorPassword)
	t.Setenv("APP_USER_PASSWORD", appUserPassword)
	runMigrations(t, migratorURL)

	appCfg, err := pgxpool.ParseConfig(withUser(t, superURL, "app_user", appUserPassword))
	require.NoError(t, err)
	appCfg.MaxConns = 1
	app, err := pgxpool.NewWithConfig(ctx, appCfg)
	require.NoError(t, err)
	t.Cleanup(app.Close)

	migrator, err := pgxpool.New(ctx, migratorURL)
	require.NoError(t, err)
	t.Cleanup(migrator.Close)

	return env{db: database.New(app), app: app, migrator: migrator}
}

// runMigrations applies up, down, up so the Down section is exercised too.
func runMigrations(t *testing.T, dsn string) {
	t.Helper()
	sqlDB, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("../../db/migrations"))
	require.NoError(t, err)
	_, err = p.Up(t.Context())
	require.NoError(t, err, "up")
	_, err = p.Down(t.Context())
	require.NoError(t, err, "down")
	_, err = p.Up(t.Context())
	require.NoError(t, err, "up again")
}

func withUser(t *testing.T, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.User = url.UserPassword(user, password)
	return u.String()
}

type seeded struct {
	tenantA, tenantB uuid.UUID
	userA, userB     uuid.UUID
}

// seed inserts one tenant, owner user and refresh token per tenant through app_user.
func seed(t *testing.T, e env) seeded {
	t.Helper()
	ctx := t.Context()
	s := seeded{tenantA: uuid.Must(uuid.NewV7()), tenantB: uuid.Must(uuid.NewV7()),
		userA: uuid.Must(uuid.NewV7()), userB: uuid.Must(uuid.NewV7())}

	for _, row := range []struct {
		tenant, user uuid.UUID
		slug, email  string
	}{
		{s.tenantA, s.userA, "acme", "alice@acme.test"},
		{s.tenantB, s.userB, "beta", "bob@beta.test"},
	} {
		err := e.db.WithTenantTx(ctx, row.tenant, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $2)`,
				row.tenant, row.slug); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, tenant_id, email, password_hash, full_name, role)
				VALUES ($1, $2, $3, 'hash', 'Owner', 'owner')`, row.user, row.tenant, row.email); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO refresh_tokens (id, user_id, tenant_id, token_hash, family_id, expires_at)
				VALUES ($1, $2, $3, $4, $1, now() + interval '7 days')`,
				uuid.Must(uuid.NewV7()), row.user, row.tenant, row.slug+"-token")
			return err
		})
		require.NoError(t, err)
	}
	return s
}

func count(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, table string) int {
	t.Helper()
	var n int
	// Deliberately no WHERE tenant_id: only RLS filters these rows.
	require.NoError(t, q.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func requireSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, code, pgErr.Code, pgErr.Message)
}

func TestTenantIsolation(t *testing.T) {
	e := setup(t)
	s := seed(t, e)
	ctx := t.Context()

	t.Run("query without tenant filter sees only own tenant", func(t *testing.T) {
		err := e.db.WithTenantTx(ctx, s.tenantA, func(tx pgx.Tx) error {
			assert.Equal(t, 1, count(t, tx, "users"))
			assert.Equal(t, 1, count(t, tx, "refresh_tokens"))
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("no tenant set sees nothing and tenant does not leak to next use of the connection", func(t *testing.T) {
		// The pool has one connection, which the previous subtest used with tenant A.
		assert.Equal(t, 0, count(t, e.app, "users"))
		assert.Equal(t, 0, count(t, e.app, "refresh_tokens"))
	})

	t.Run("other tenant's row by id is not found", func(t *testing.T) {
		err := e.db.WithTenantTx(ctx, s.tenantA, func(tx pgx.Tx) error {
			var id uuid.UUID
			return tx.QueryRow(ctx, "SELECT id FROM users WHERE id = $1", s.userB).Scan(&id)
		})
		assert.ErrorIs(t, err, pgx.ErrNoRows)
	})

	t.Run("cannot update other tenant's row", func(t *testing.T) {
		err := e.db.WithTenantTx(ctx, s.tenantA, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, "UPDATE users SET full_name = 'hacked' WHERE id = $1", s.userB)
			assert.Zero(t, tag.RowsAffected())
			return err
		})
		require.NoError(t, err)
	})

	t.Run("cannot insert a row for another tenant", func(t *testing.T) {
		err := e.db.WithTenantTx(ctx, s.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO users (id, tenant_id, email, password_hash, full_name, role)
				VALUES ($1, $2, 'mallory@beta.test', 'h', 'M', 'member')`, uuid.Must(uuid.NewV7()), s.tenantB)
			return err
		})
		requireSQLState(t, err, "42501") // RLS WITH CHECK violation
	})

	t.Run("refresh token cannot point at another tenant's user", func(t *testing.T) {
		err := e.db.WithTenantTx(ctx, s.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO refresh_tokens (id, user_id, tenant_id, token_hash, family_id, expires_at)
				VALUES ($1, $2, $3, 'cross', $1, now())`, uuid.Must(uuid.NewV7()), s.userB, s.tenantA)
			return err
		})
		requireSQLState(t, err, "23503") // composite FK violation
	})

	t.Run("app_user has no DELETE privilege", func(t *testing.T) {
		err := e.db.WithTenantTx(ctx, s.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DELETE FROM users")
			return err
		})
		requireSQLState(t, err, "42501")
	})

	t.Run("FORCE applies RLS to the table owner too", func(t *testing.T) {
		assert.Equal(t, 0, count(t, e.migrator, "users"))
	})

	t.Run("login lookup finds user across tenants without a tenant set", func(t *testing.T) {
		var id, tenantID uuid.UUID
		var hash, role string
		err := e.app.QueryRow(ctx,
			"SELECT id, tenant_id, password_hash, role FROM auth_find_user_by_email($1)", "BOB@beta.test",
		).Scan(&id, &tenantID, &hash, &role)
		require.NoError(t, err)
		assert.Equal(t, s.userB, id)
		assert.Equal(t, s.tenantB, tenantID)

		// The function's policy must not open the table to app_user outside the function.
		_, err = e.app.Exec(ctx, "SET app.auth_lookup = 'on'")
		require.NoError(t, err)
		assert.Equal(t, 0, count(t, e.app, "users"))
		_, err = e.app.Exec(ctx, "RESET app.auth_lookup")
		require.NoError(t, err)
	})

	t.Run("WithTenantTx rejects nil tenant and rolls back on error", func(t *testing.T) {
		err := e.db.WithTenantTx(ctx, uuid.Nil, func(pgx.Tx) error { return nil })
		require.ErrorIs(t, err, database.ErrNoTenant)

		boom := errors.New("boom")
		err = e.db.WithTenantTx(ctx, s.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "UPDATE users SET full_name = 'changed' WHERE id = $1", s.userA)
			require.NoError(t, err)
			return boom
		})
		require.ErrorIs(t, err, boom)

		err = e.db.WithTenantTx(ctx, s.tenantA, func(tx pgx.Tx) error {
			var name string
			require.NoError(t, tx.QueryRow(ctx, "SELECT full_name FROM users WHERE id = $1", s.userA).Scan(&name))
			assert.Equal(t, "Owner", name)
			return nil
		})
		require.NoError(t, err)
	})
}
