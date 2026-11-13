//go:build integration

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

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

	"github.com/HieuTrannn/crm-backend/internal/auth"
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

// execer is what both a pool and a transaction offer for running a statement.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// TestTenantIsolation proves NFR-ISO-01..04 at the database level. Each row runs one
// statement as app_user (inside A's WithTenantTx, or outside any tenant) or as the
// table owner, and checks the rows returned/affected or the SQLSTATE raised.
func TestTenantIsolation(t *testing.T) {
	e := setup(t)
	s := seed(t, e)
	ctx := t.Context()
	newID := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	tenantA := &s.tenantA

	tests := []struct {
		nfr      string
		name     string
		tenant   *uuid.UUID // nil: run outside WithTenantTx
		asOwner  bool       // run as the table owner instead of app_user
		sql      string
		args     []any
		wantRows int64  // rows returned (SELECT) or affected (UPDATE)
		wantCode string // expected SQLSTATE; empty means the statement succeeds
	}{
		{nfr: "NFR-ISO-02", name: "users without WHERE tenant_id returns only A's row",
			tenant: tenantA, sql: "SELECT id FROM users", wantRows: 1},
		{nfr: "NFR-ISO-02", name: "the row returned is A's own user",
			tenant: tenantA, sql: "SELECT id FROM users WHERE id = $1", args: []any{s.userA}, wantRows: 1},
		{nfr: "NFR-ISO-02", name: "refresh_tokens without WHERE tenant_id returns only A's row",
			tenant: tenantA, sql: "SELECT id FROM refresh_tokens", wantRows: 1},
		// The app pool has a single connection that the rows above just used with tenant A,
		// so these also prove set_config(..., true) does not leak to the next use.
		{nfr: "NFR-ISO-02", name: "users outside WithTenantTx returns nothing (fail closed)",
			sql: "SELECT id FROM users", wantRows: 0},
		{nfr: "NFR-ISO-02", name: "refresh_tokens outside WithTenantTx returns nothing (fail closed)",
			sql: "SELECT id FROM refresh_tokens", wantRows: 0},
		{nfr: "NFR-ISO-04", name: "B's user by id is not found from A",
			tenant: tenantA, sql: "SELECT id FROM users WHERE id = $1", args: []any{s.userB}, wantRows: 0},
		{nfr: "NFR-ISO-01", name: "updating B's user from A affects nothing",
			tenant: tenantA, sql: "UPDATE users SET full_name = 'hacked' WHERE id = $1", args: []any{s.userB}, wantRows: 0},
		{nfr: "NFR-ISO-01", name: "INSERT with B's tenant_id is rejected by WITH CHECK",
			tenant: tenantA, wantCode: "42501",
			sql: `INSERT INTO users (id, tenant_id, email, password_hash, full_name, role)
				VALUES ($1, $2, 'mallory@beta.test', 'h', 'M', 'member')`, args: []any{newID(), s.tenantB}},
		{nfr: "NFR-ISO-01", name: "refresh token cannot reference B's user (composite FK)",
			tenant: tenantA, wantCode: "23503",
			sql: `INSERT INTO refresh_tokens (id, user_id, tenant_id, token_hash, family_id, expires_at)
				VALUES ($1, $2, $3, 'cross', $1, now())`, args: []any{newID(), s.userB, s.tenantA}},
		{nfr: "NFR-ISO-03", name: "app_user has no DELETE privilege",
			tenant: tenantA, sql: "DELETE FROM users", wantCode: "42501"},
		{nfr: "NFR-ISO-03", name: "FORCE applies RLS to the table owner too",
			asOwner: true, sql: "SELECT id FROM users", wantRows: 0},
	}
	for _, tt := range tests {
		t.Run(tt.nfr+"/"+tt.name, func(t *testing.T) {
			var tag pgconn.CommandTag
			run := func(q execer) error {
				var err error
				tag, err = q.Exec(ctx, tt.sql, tt.args...)
				return err
			}

			var err error
			switch {
			case tt.asOwner:
				err = run(e.migrator)
			case tt.tenant == nil:
				err = run(e.app)
			default:
				err = e.db.WithTenantTx(ctx, *tt.tenant, func(tx pgx.Tx) error { return run(tx) })
			}

			if tt.wantCode != "" {
				requireSQLState(t, err, tt.wantCode)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantRows, tag.RowsAffected())
		})
	}

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

// TestTenantIsolationAPI proves NFR-ISO-01/04 through HTTP. Even an access token
// whose claims pair a user with the wrong tenant (forged, or a bug upstream)
// cannot read that user: the answer is 404, never another tenant's data or a 403.
func TestTenantIsolationAPI(t *testing.T) {
	e := setup(t)
	s := seed(t, e)
	h := newAuthRouter(t, e)
	tokens := auth.NewTokenIssuer(testJWTSecret)
	accessCookie := func(user, tenant uuid.UUID) *http.Cookie {
		tok, _, err := tokens.Issue(auth.Principal{UserID: user, TenantID: tenant, Role: auth.RoleOwner}, time.Now())
		require.NoError(t, err)
		return &http.Cookie{Name: "access_token", Value: tok}
	}

	tests := []struct {
		nfr        string
		name       string
		cookie     *http.Cookie
		wantStatus int
		wantUser   uuid.UUID
		wantTenant uuid.UUID
	}{
		{nfr: "NFR-ISO-01", name: "A reads its own user",
			cookie: accessCookie(s.userA, s.tenantA), wantStatus: http.StatusOK, wantUser: s.userA, wantTenant: s.tenantA},
		{nfr: "NFR-ISO-04", name: "B's user under A's tenant is 404",
			cookie: accessCookie(s.userB, s.tenantA), wantStatus: http.StatusNotFound},
		{nfr: "NFR-ISO-04", name: "A's user under B's tenant is 404",
			cookie: accessCookie(s.userA, s.tenantB), wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.nfr+"/"+tt.name, func(t *testing.T) {
			rec := call(t, h, http.MethodGet, "/api/v1/me", nil, tt.cookie)

			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			if tt.wantStatus != http.StatusOK {
				assert.Equal(t, "NOT_FOUND", errorCode(t, rec))
				assert.NotContains(t, rec.Body.String(), "bob@beta.test")
				return
			}
			var me auth.Me
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
			assert.Equal(t, tt.wantUser, me.ID)
			assert.Equal(t, tt.wantTenant, me.Tenant.ID)
		})
	}
}
