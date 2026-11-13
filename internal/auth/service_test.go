package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// fakeRepo is an in-memory Repository. Transactions and RLS are covered by the
// integration tests; here only the service's business rules are under test.
type fakeRepo struct {
	users         map[string]Credentials // by lower-case email
	tokens        map[string]*fakeToken  // by token hash
	slugs         map[string]bool
	createUserErr error
}

type fakeToken struct {
	RefreshToken
	tenantID uuid.UUID
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: map[string]Credentials{}, tokens: map[string]*fakeToken{}, slugs: map[string]bool{}}
}

func (f *fakeRepo) WithTenant(_ context.Context, tenantID uuid.UUID, fn func(TenantStore) error) error {
	return fn(&fakeStore{repo: f, tenantID: tenantID})
}

func (f *fakeRepo) FindUserByEmail(_ context.Context, email string) (Credentials, error) {
	c, ok := f.users[strings.ToLower(email)]
	if !ok {
		return Credentials{}, ErrNotFound
	}
	return c, nil
}

type fakeStore struct {
	repo     *fakeRepo
	tenantID uuid.UUID
}

func (s *fakeStore) CreateTenant(_ context.Context, t TenantInfo) error {
	if s.repo.slugs[t.Slug] {
		return ErrSlugTaken
	}
	s.repo.slugs[t.Slug] = true
	return nil
}

func (s *fakeStore) CreateUser(_ context.Context, u NewUser) error {
	if s.repo.createUserErr != nil {
		return s.repo.createUserErr
	}
	s.repo.users[strings.ToLower(u.Email)] = Credentials{UserID: u.ID, TenantID: s.tenantID, PasswordHash: u.PasswordHash, Role: u.Role}
	return nil
}

func (s *fakeStore) GetMe(_ context.Context, userID uuid.UUID) (Me, error) {
	for email, c := range s.repo.users {
		if c.UserID == userID && c.TenantID == s.tenantID {
			return Me{ID: userID, Email: email, Role: c.Role, Tenant: TenantInfo{ID: s.tenantID}}, nil
		}
	}
	return Me{}, ErrNotFound
}

func (s *fakeStore) CreateRefreshToken(_ context.Context, userID uuid.UUID, hash string, familyID uuid.UUID, exp time.Time) error {
	role := ""
	for _, c := range s.repo.users {
		if c.UserID == userID {
			role = c.Role
		}
	}
	s.repo.tokens[hash] = &fakeToken{
		RefreshToken: RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: hash, FamilyID: familyID, ExpiresAt: exp, Role: role},
		tenantID:     s.tenantID,
	}
	return nil
}

func (s *fakeStore) GetRefreshToken(_ context.Context, hash string) (RefreshToken, error) {
	t, ok := s.repo.tokens[hash]
	if !ok || t.tenantID != s.tenantID {
		return RefreshToken{}, ErrNotFound
	}
	return t.RefreshToken, nil
}

func (s *fakeStore) RevokeRefreshToken(_ context.Context, id uuid.UUID) (bool, error) {
	for _, t := range s.repo.tokens {
		if t.ID == id && t.RevokedAt == nil {
			now := time.Now()
			t.RevokedAt = &now
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) RevokeFamily(_ context.Context, familyID uuid.UUID) error {
	for _, t := range s.repo.tokens {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			now := time.Now()
			t.RevokedAt = &now
		}
	}
	return nil
}

func newTestService(t *testing.T) (*Service, *fakeRepo) {
	t.Helper()
	repo := newFakeRepo()
	svc, err := NewService(repo, NewTokenIssuer(testSecret), NewMemoryLimiter(5, 3*time.Minute), bcrypt.MinCost)
	require.NoError(t, err)
	return svc, repo
}

func registerOwner(t *testing.T, svc *Service) Me {
	t.Helper()
	me, err := svc.Register(t.Context(), RegisterInput{
		CompanyName: "Acme", Email: "alice@acme.test", Password: "s3cret-pass", FullName: "Alice",
	})
	require.NoError(t, err)
	return me
}

func TestRegister(t *testing.T) {
	svc, repo := newTestService(t)

	me := registerOwner(t, svc)

	assert.Equal(t, RoleOwner, me.Role)
	assert.Equal(t, "acme", me.Tenant.Slug)
	stored := repo.users["alice@acme.test"]
	assert.Equal(t, me.Tenant.ID, stored.TenantID)
	assert.NotEqual(t, "s3cret-pass", stored.PasswordHash)
	assert.True(t, VerifyPassword(stored.PasswordHash, "s3cret-pass"))
}

func TestRegister_SameCompanyNameGetsSuffixedSlug(t *testing.T) {
	svc, _ := newTestService(t)
	first := registerOwner(t, svc)

	second, err := svc.Register(t.Context(), RegisterInput{
		CompanyName: "ACME", Email: "bob@acme.test", Password: "s3cret-pass", FullName: "Bob",
	})

	require.NoError(t, err)
	assert.Equal(t, "acme", first.Tenant.Slug)
	assert.Regexp(t, `^acme-[a-z2-7]{6}$`, second.Tenant.Slug)
}

func TestRegister_EmailTaken(t *testing.T) {
	svc, repo := newTestService(t)
	repo.createUserErr = ErrEmailTaken

	_, err := svc.Register(t.Context(), RegisterInput{Email: "a@b.io", Password: "s3cret-pass"})

	require.ErrorIs(t, err, ErrEmailTaken)
}

func TestLogin(t *testing.T) {
	svc, _ := newTestService(t)
	me := registerOwner(t, svc)

	sess, err := svc.Login(t.Context(), "ALICE@acme.test", "s3cret-pass", "1.2.3.4")
	require.NoError(t, err)

	p, err := svc.tokens.Parse(sess.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, Principal{UserID: me.ID, TenantID: me.Tenant.ID, Role: RoleOwner}, p)
	tenantID, _, err := parseRefreshCookie(sess.RefreshCookie)
	require.NoError(t, err)
	assert.Equal(t, me.Tenant.ID, tenantID)
}

func TestLogin_WrongPasswordAndUnknownEmailLookTheSame(t *testing.T) {
	svc, _ := newTestService(t)
	registerOwner(t, svc)

	_, errWrong := svc.Login(t.Context(), "alice@acme.test", "wrong-pass", "1.2.3.4")
	_, errUnknown := svc.Login(t.Context(), "nobody@acme.test", "s3cret-pass", "1.2.3.4")

	require.ErrorIs(t, errWrong, ErrInvalidCredentials)
	require.ErrorIs(t, errUnknown, ErrInvalidCredentials)
}

func TestLogin_RateLimited(t *testing.T) {
	svc, _ := newTestService(t)
	registerOwner(t, svc)

	for range 5 {
		_, err := svc.Login(t.Context(), "alice@acme.test", "wrong-pass", "1.2.3.4")
		require.ErrorIs(t, err, ErrInvalidCredentials)
	}
	_, err := svc.Login(t.Context(), "alice@acme.test", "s3cret-pass", "1.2.3.4")
	require.ErrorIs(t, err, ErrTooManyAttempts, "even the right password is refused while limited")

	_, err = svc.Login(t.Context(), "alice@acme.test", "s3cret-pass", "5.6.7.8")
	require.NoError(t, err, "another IP is not affected")
}

func TestRefresh_RotatesAndDetectsReuse(t *testing.T) {
	svc, _ := newTestService(t)
	registerOwner(t, svc)
	first, err := svc.Login(t.Context(), "alice@acme.test", "s3cret-pass", "1.2.3.4")
	require.NoError(t, err)

	second, err := svc.Refresh(t.Context(), first.RefreshCookie)
	require.NoError(t, err)
	assert.NotEqual(t, first.RefreshCookie, second.RefreshCookie)

	_, err = svc.Refresh(t.Context(), first.RefreshCookie)
	require.ErrorIs(t, err, ErrRefreshTokenReused)

	_, err = svc.Refresh(t.Context(), second.RefreshCookie)
	require.ErrorIs(t, err, ErrRefreshTokenReused, "reuse revoked the whole family, including the newest token")
}

func TestRefresh_Rejected(t *testing.T) {
	svc, _ := newTestService(t)
	registerOwner(t, svc)
	sess, err := svc.Login(t.Context(), "alice@acme.test", "s3cret-pass", "1.2.3.4")
	require.NoError(t, err)
	_, raw, err := parseRefreshCookie(sess.RefreshCookie)
	require.NoError(t, err)

	_, err = svc.Refresh(t.Context(), refreshCookieValue(uuid.Must(uuid.NewV7()), raw))
	require.ErrorIs(t, err, ErrInvalidRefreshToken, "token presented under another tenant")

	_, err = svc.Refresh(t.Context(), "garbage")
	require.ErrorIs(t, err, ErrInvalidRefreshToken)

	svc.now = func() time.Time { return time.Now().Add(RefreshTokenTTL + time.Minute) }
	_, err = svc.Refresh(t.Context(), sess.RefreshCookie)
	require.ErrorIs(t, err, ErrInvalidRefreshToken, "expired")
}

func TestMe(t *testing.T) {
	svc, _ := newTestService(t)
	owner := registerOwner(t, svc)

	me, err := svc.Me(t.Context(), Principal{UserID: owner.ID, TenantID: owner.Tenant.ID, Role: RoleOwner})
	require.NoError(t, err)
	assert.Equal(t, owner.ID, me.ID)

	_, err = svc.Me(t.Context(), Principal{UserID: owner.ID, TenantID: uuid.Must(uuid.NewV7())})
	require.ErrorIs(t, err, ErrNotFound, "same user id under another tenant is not found")
}

func TestLogout_RevokesSession(t *testing.T) {
	svc, _ := newTestService(t)
	registerOwner(t, svc)
	sess, err := svc.Login(t.Context(), "alice@acme.test", "s3cret-pass", "1.2.3.4")
	require.NoError(t, err)

	require.NoError(t, svc.Logout(t.Context(), sess.RefreshCookie))
	require.NoError(t, svc.Logout(t.Context(), "garbage"), "logout never fails on a bad cookie")

	_, err = svc.Refresh(t.Context(), sess.RefreshCookie)
	require.Error(t, err)
}
