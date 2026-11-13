package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Repository is the persistence the service needs (declared here, implemented in repository.go).
type Repository interface {
	// WithTenant runs fn in one transaction scoped to tenantID (RLS). fn's error rolls it back.
	WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(TenantStore) error) error
	// FindUserByEmail is the only cross-tenant lookup, used by login. Missing user: ErrNotFound.
	FindUserByEmail(ctx context.Context, email string) (Credentials, error)
}

// TenantStore is the set of queries available inside a tenant transaction.
type TenantStore interface {
	CreateTenant(ctx context.Context, t TenantInfo) error
	CreateUser(ctx context.Context, u NewUser) error
	GetMe(ctx context.Context, userID uuid.UUID) (Me, error)
	CreateRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, familyID uuid.UUID, expiresAt time.Time) error
	GetRefreshToken(ctx context.Context, tokenHash string) (RefreshToken, error)
	// RevokeRefreshToken returns false when the token was already revoked (lost a race or reused).
	RevokeRefreshToken(ctx context.Context, id uuid.UUID) (bool, error)
	RevokeFamily(ctx context.Context, familyID uuid.UUID) error
}

// Service implements the auth use cases.
type Service struct {
	repo       Repository
	tokens     *TokenIssuer
	limiter    LoginLimiter
	now        func() time.Time
	bcryptCost int
	dummyHash  string
}

// NewService wires the auth service. bcryptCost is BcryptCost in production;
// tests may pass bcrypt.MinCost to stay fast.
func NewService(repo Repository, tokens *TokenIssuer, limiter LoginLimiter, bcryptCost int) (*Service, error) {
	// Hash compared against when the email does not exist, so a login for an
	// unknown email costs the same time as a wrong password (no user enumeration).
	dummy, err := HashPassword("dummy-password-for-timing", bcryptCost)
	if err != nil {
		return nil, err
	}
	return &Service{repo: repo, tokens: tokens, limiter: limiter, now: time.Now,
		bcryptCost: bcryptCost, dummyHash: dummy}, nil
}

// Register creates the tenant and its owner in a single transaction (FR-AUTH-01).
func (s *Service) Register(ctx context.Context, in RegisterInput) (Me, error) {
	// Hash before opening the transaction: bcrypt is slow and must not hold a connection.
	hash, err := HashPassword(in.Password, s.bcryptCost)
	if err != nil {
		return Me{}, err
	}
	tenantID, err := uuid.NewV7()
	if err != nil {
		return Me{}, fmt.Errorf("new tenant id: %w", err)
	}
	userID, err := uuid.NewV7()
	if err != nil {
		return Me{}, fmt.Errorf("new user id: %w", err)
	}

	tenant := TenantInfo{ID: tenantID, Name: in.CompanyName, Slug: in.Slug}
	user := NewUser{ID: userID, TenantID: tenantID, Email: in.Email, PasswordHash: hash,
		FullName: in.FullName, Role: RoleOwner}
	err = s.repo.WithTenant(ctx, tenantID, func(st TenantStore) error {
		if err := st.CreateTenant(ctx, tenant); err != nil {
			return err
		}
		return st.CreateUser(ctx, user)
	})
	if err != nil {
		return Me{}, fmt.Errorf("register: %w", err)
	}
	return Me{ID: userID, Email: in.Email, FullName: in.FullName, Role: RoleOwner, Tenant: tenant}, nil
}

// Login checks credentials and starts a new session (new token family).
func (s *Service) Login(ctx context.Context, email, password, clientIP string) (Session, error) {
	key := clientIP + "|" + strings.ToLower(email)
	allowed, err := s.limiter.Allow(ctx, key)
	if err != nil {
		return Session{}, fmt.Errorf("rate limit: %w", err)
	}
	if !allowed {
		return Session{}, ErrTooManyAttempts
	}

	cred, err := s.repo.FindUserByEmail(ctx, email)
	switch {
	case errors.Is(err, ErrNotFound):
		VerifyPassword(s.dummyHash, password) // same cost as a real check
		return Session{}, s.loginFailed(ctx, key)
	case err != nil:
		return Session{}, fmt.Errorf("find user: %w", err)
	case !VerifyPassword(cred.PasswordHash, password):
		return Session{}, s.loginFailed(ctx, key)
	}

	familyID, err := uuid.NewV7()
	if err != nil {
		return Session{}, fmt.Errorf("new family id: %w", err)
	}
	p := Principal{UserID: cred.UserID, TenantID: cred.TenantID, Role: cred.Role}
	var sess Session
	err = s.repo.WithTenant(ctx, cred.TenantID, func(st TenantStore) error {
		var err error
		sess, err = s.newSession(ctx, st, p, familyID)
		return err
	})
	if err != nil {
		return Session{}, fmt.Errorf("login: %w", err)
	}
	return sess, nil
}

func (s *Service) loginFailed(ctx context.Context, key string) error {
	if err := s.limiter.Fail(ctx, key); err != nil {
		return fmt.Errorf("rate limit: %w", err)
	}
	return ErrInvalidCredentials
}

// Refresh rotates the refresh token (FR-AUTH-04). Presenting a token that was
// already rotated or revoked means it was copied: the whole family is revoked.
func (s *Service) Refresh(ctx context.Context, cookie string) (Session, error) {
	tenantID, raw, err := parseRefreshCookie(cookie)
	if err != nil {
		return Session{}, err
	}
	hash := hashRefreshToken(raw)

	var sess Session
	reused := false
	err = s.repo.WithTenant(ctx, tenantID, func(st TenantStore) error {
		rt, err := s.findRefreshToken(ctx, st, hash)
		if err != nil {
			return err
		}
		if rt.RevokedAt != nil {
			reused = true
			// Return nil so the family revocation is committed, not rolled back.
			return st.RevokeFamily(ctx, rt.FamilyID)
		}
		if !s.now().Before(rt.ExpiresAt) {
			return ErrInvalidRefreshToken
		}
		// Conditional UPDATE: of two concurrent requests with the same token, only one wins.
		claimed, err := st.RevokeRefreshToken(ctx, rt.ID)
		if err != nil {
			return err
		}
		if !claimed {
			reused = true
			return st.RevokeFamily(ctx, rt.FamilyID)
		}
		p := Principal{UserID: rt.UserID, TenantID: tenantID, Role: rt.Role}
		sess, err = s.newSession(ctx, st, p, rt.FamilyID)
		return err
	})
	if err != nil {
		return Session{}, fmt.Errorf("refresh: %w", err)
	}
	if reused {
		return Session{}, ErrRefreshTokenReused
	}
	return sess, nil
}

// Logout revokes the session's whole token family (FR-AUTH-05). An unknown or
// malformed cookie is not an error: the caller is logged out either way.
func (s *Service) Logout(ctx context.Context, cookie string) error {
	tenantID, raw, err := parseRefreshCookie(cookie)
	if err != nil {
		return nil
	}
	hash := hashRefreshToken(raw)
	err = s.repo.WithTenant(ctx, tenantID, func(st TenantStore) error {
		rt, err := s.findRefreshToken(ctx, st, hash)
		if errors.Is(err, ErrInvalidRefreshToken) {
			return nil
		}
		if err != nil {
			return err
		}
		return st.RevokeFamily(ctx, rt.FamilyID)
	})
	if err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// Me returns the caller's profile and tenant.
func (s *Service) Me(ctx context.Context, p Principal) (Me, error) {
	var me Me
	err := s.repo.WithTenant(ctx, p.TenantID, func(st TenantStore) error {
		var err error
		me, err = st.GetMe(ctx, p.UserID)
		return err
	})
	if err != nil {
		return Me{}, fmt.Errorf("me: %w", err)
	}
	return me, nil
}

// findRefreshToken looks the token up by hash. The database lookup already
// matched the hash; the constant-time re-check guards against a store whose
// lookup is not an exact match (e.g. a case-insensitive or prefix index).
func (s *Service) findRefreshToken(ctx context.Context, st TenantStore, hash string) (RefreshToken, error) {
	rt, err := st.GetRefreshToken(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return RefreshToken{}, ErrInvalidRefreshToken
	}
	if err != nil {
		return RefreshToken{}, err
	}
	if !tokenHashEqual(rt.TokenHash, hash) {
		return RefreshToken{}, ErrInvalidRefreshToken
	}
	return rt, nil
}

// newSession issues an access token and stores a new refresh token in familyID.
func (s *Service) newSession(ctx context.Context, st TenantStore, p Principal, familyID uuid.UUID) (Session, error) {
	now := s.now()
	access, accessExp, err := s.tokens.Issue(p, now)
	if err != nil {
		return Session{}, err
	}
	raw, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	refreshExp := now.Add(RefreshTokenTTL)
	if err := st.CreateRefreshToken(ctx, p.UserID, hashRefreshToken(raw), familyID, refreshExp); err != nil {
		return Session{}, err
	}
	return Session{
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshCookie:    refreshCookieValue(p.TenantID, raw),
		RefreshExpiresAt: refreshExp,
	}, nil
}
