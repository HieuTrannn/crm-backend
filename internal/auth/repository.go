package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HieuTrannn/crm-backend/db/sqlc"
	"github.com/HieuTrannn/crm-backend/internal/platform/database"
)

// PostgresRepository implements Repository on top of sqlc.
type PostgresRepository struct {
	db   *database.DB
	pool *pgxpool.Pool
}

// NewPostgresRepository creates the repository. pool is used only for the
// cross-tenant login lookup; everything else goes through db.WithTenantTx.
func NewPostgresRepository(db *database.DB, pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db, pool: pool}
}

// WithTenant implements Repository.
func (r *PostgresRepository) WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(TenantStore) error) error {
	return r.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return fn(&tenantStore{q: sqlc.New(tx), tenantID: tenantID})
	})
}

// FindUserByEmail implements Repository via the SECURITY DEFINER function
// (sqlc cannot type its result, so this one query is hand-written; still parameterized).
func (r *PostgresRepository) FindUserByEmail(ctx context.Context, email string) (Credentials, error) {
	var c Credentials
	err := r.pool.QueryRow(ctx,
		"SELECT id, tenant_id, password_hash, role FROM auth_find_user_by_email($1)", email,
	).Scan(&c.UserID, &c.TenantID, &c.PasswordHash, &c.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credentials{}, ErrNotFound
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("find user by email: %w", err)
	}
	return c, nil
}

// tenantStore runs sqlc queries inside one tenant transaction. Every query also
// filters by tenantID explicitly (layer 1); RLS is layer 2.
type tenantStore struct {
	q        *sqlc.Queries
	tenantID uuid.UUID
}

func (s *tenantStore) CreateTenant(ctx context.Context, t TenantInfo) error {
	err := s.q.CreateTenant(ctx, sqlc.CreateTenantParams{ID: t.ID, Name: t.Name, Slug: t.Slug})
	return mapUniqueViolation(err, "tenants_slug_key", ErrSlugTaken)
}

func (s *tenantStore) CreateUser(ctx context.Context, u NewUser) error {
	err := s.q.CreateUser(ctx, sqlc.CreateUserParams{
		ID: u.ID, TenantID: s.tenantID, Email: u.Email,
		PasswordHash: u.PasswordHash, FullName: u.FullName, Role: u.Role,
	})
	return mapUniqueViolation(err, "users_email_key", ErrEmailTaken)
}

func (s *tenantStore) GetMe(ctx context.Context, userID uuid.UUID) (Me, error) {
	row, err := s.q.GetUserWithTenant(ctx, sqlc.GetUserWithTenantParams{ID: userID, TenantID: s.tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Me{}, ErrNotFound
	}
	if err != nil {
		return Me{}, fmt.Errorf("get user: %w", err)
	}
	return Me{
		ID: row.ID, Email: row.Email, FullName: row.FullName, Role: row.Role,
		Tenant: TenantInfo{ID: row.TenantID, Name: row.TenantName, Slug: row.TenantSlug},
	}, nil
}

func (s *tenantStore) CreateRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, familyID uuid.UUID, expiresAt time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("new refresh token id: %w", err)
	}
	err = s.q.CreateRefreshToken(ctx, sqlc.CreateRefreshTokenParams{
		ID: id, UserID: userID, TenantID: s.tenantID, TokenHash: tokenHash,
		FamilyID: familyID, ExpiresAt: expiresAt,
	})
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return nil
}

func (s *tenantStore) GetRefreshToken(ctx context.Context, tokenHash string) (RefreshToken, error) {
	row, err := s.q.GetRefreshTokenByHash(ctx, sqlc.GetRefreshTokenByHashParams{TokenHash: tokenHash, TenantID: s.tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshToken{}, ErrNotFound
	}
	if err != nil {
		return RefreshToken{}, fmt.Errorf("get refresh token: %w", err)
	}
	return RefreshToken{
		ID: row.ID, UserID: row.UserID, TokenHash: row.TokenHash, FamilyID: row.FamilyID,
		ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt, Role: row.Role,
	}, nil
}

func (s *tenantStore) RevokeRefreshToken(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := s.q.RevokeRefreshToken(ctx, sqlc.RevokeRefreshTokenParams{ID: id, TenantID: s.tenantID})
	if err != nil {
		return false, fmt.Errorf("revoke refresh token: %w", err)
	}
	return n == 1, nil
}

func (s *tenantStore) RevokeFamily(ctx context.Context, familyID uuid.UUID) error {
	err := s.q.RevokeRefreshTokenFamily(ctx, sqlc.RevokeRefreshTokenFamilyParams{FamilyID: familyID, TenantID: s.tenantID})
	if err != nil {
		return fmt.Errorf("revoke token family: %w", err)
	}
	return nil
}

// mapUniqueViolation turns a violation of the named unique constraint into target.
func mapUniqueViolation(err error, constraint string, target error) error {
	if database.ViolatesConstraint(err, constraint) {
		return target
	}
	return err
}
