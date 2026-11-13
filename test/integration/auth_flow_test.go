//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HieuTrannn/crm-backend/internal/auth"
	"github.com/HieuTrannn/crm-backend/internal/server"
)

// testJWTSecret signs access tokens in integration tests.
const testJWTSecret = "integration-secret-at-least-32-bytes!"

func newAuthRouter(t *testing.T, e env) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tokens := auth.NewTokenIssuer(testJWTSecret)
	svc, err := auth.NewService(auth.NewPostgresRepository(e.db, e.app), tokens,
		auth.NewMemoryLimiter(5, 3*time.Minute), auth.BcryptCost)
	require.NoError(t, err)
	return server.NewRouter(slog.New(slog.DiscardHandler), e.app, auth.NewHandler(svc, tokens, false))
}

func call(t *testing.T, h http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %q not set", name)
	return nil
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body.Error.Code
}

type registerBody struct {
	CompanyName string `json:"company_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	FullName    string `json:"full_name"`
}

func register(t *testing.T, h http.Handler, company, email string) auth.Me {
	t.Helper()
	rec := call(t, h, http.MethodPost, "/api/v1/auth/register",
		registerBody{CompanyName: company, Email: email, Password: "s3cret-pass", FullName: "Owner"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Empty(t, rec.Result().Cookies(), "register does not log in")
	var me auth.Me
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
	return me
}

func login(t *testing.T, h http.Handler, email string) (access, refresh *http.Cookie) {
	t.Helper()
	rec := call(t, h, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": "s3cret-pass"})
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	return cookie(t, rec, "access_token"), cookie(t, rec, "refresh_token")
}

func TestAuthFlow(t *testing.T) {
	h := newAuthRouter(t, setup(t))

	acme := register(t, h, "Acme", "alice@acme.test")
	assert.Equal(t, "owner", acme.Role)
	assert.Equal(t, "acme", acme.Tenant.Slug)

	t.Run("slug comes from the company name, with a suffix when taken", func(t *testing.T) {
		vn := register(t, h, "Công ty TNHH Đại Phát", "owner@daiphat.test")
		assert.Equal(t, "cong-ty-tnhh-dai-phat", vn.Tenant.Slug)
		assert.Equal(t, "Công ty TNHH Đại Phát", vn.Tenant.Name)

		again := register(t, h, "ACME", "second@acme.test")
		assert.Regexp(t, `^acme-[a-z2-7]{6}$`, again.Tenant.Slug)
	})

	t.Run("duplicate email is 409 and creates nothing", func(t *testing.T) {
		rec := call(t, h, http.MethodPost, "/api/v1/auth/register",
			registerBody{CompanyName: "Fresh Co", Email: "ALICE@acme.test", Password: "s3cret-pass", FullName: "X"})
		require.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, "EMAIL_TAKEN", errorCode(t, rec))

		// The failed attempt's tenant was rolled back with its user, so the plain slug is still free.
		fresh := register(t, h, "Fresh Co", "someone@fresh.test")
		assert.Equal(t, "fresh-co", fresh.Tenant.Slug)
	})

	t.Run("invalid input is 400 with field details", func(t *testing.T) {
		rec := call(t, h, http.MethodPost, "/api/v1/auth/register",
			registerBody{CompanyName: "X", Email: "not-an-email", Password: "short", FullName: "X"})
		require.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), `"email":"email"`)
		assert.Contains(t, rec.Body.String(), `"password":"min"`)
	})

	t.Run("wrong password is 401", func(t *testing.T) {
		rec := call(t, h, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "alice@acme.test", "password": "nope-nope"})
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, "INVALID_CREDENTIALS", errorCode(t, rec))
	})

	t.Run("login sets hardened cookies and /me works", func(t *testing.T) {
		access, refresh := login(t, h, "alice@acme.test")
		assert.True(t, access.HttpOnly)
		assert.Equal(t, http.SameSiteLaxMode, access.SameSite)
		assert.Equal(t, "/api", access.Path)
		assert.Equal(t, "/api/v1/auth", refresh.Path)
		assert.True(t, refresh.HttpOnly)

		rec := call(t, h, http.MethodGet, "/api/v1/me", nil, access)
		require.Equal(t, http.StatusOK, rec.Code)
		var me auth.Me
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
		assert.Equal(t, acme.ID, me.ID)
		assert.Equal(t, "acme", me.Tenant.Slug)

		assert.Equal(t, http.StatusUnauthorized, call(t, h, http.MethodGet, "/api/v1/me", nil).Code)
	})

	t.Run("refresh rotates; reusing the old token revokes the whole family", func(t *testing.T) {
		_, refresh1 := login(t, h, "alice@acme.test")

		rec := call(t, h, http.MethodPost, "/api/v1/auth/refresh", nil, refresh1)
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		refresh2 := cookie(t, rec, "refresh_token")
		assert.NotEqual(t, refresh1.Value, refresh2.Value)

		rec = call(t, h, http.MethodPost, "/api/v1/auth/refresh", nil, refresh1)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "reuse of a rotated token is blocked")
		assert.Equal(t, "INVALID_REFRESH_TOKEN", errorCode(t, rec))

		rec = call(t, h, http.MethodPost, "/api/v1/auth/refresh", nil, refresh2)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "the newest token died with its family")
	})

	t.Run("refresh token cannot be used under another tenant", func(t *testing.T) {
		beta := register(t, h, "Beta", "bob@beta.test")
		_, refresh := login(t, h, "bob@beta.test")
		_, raw, ok := strings.Cut(refresh.Value, ".")
		require.True(t, ok)
		require.NotEqual(t, acme.Tenant.ID, beta.Tenant.ID)

		forged := &http.Cookie{Name: "refresh_token", Value: acme.Tenant.ID.String() + "." + raw}
		rec := call(t, h, http.MethodPost, "/api/v1/auth/refresh", nil, forged)
		require.Equal(t, http.StatusUnauthorized, rec.Code)

		// The genuine cookie still works: the forged attempt found nothing to revoke.
		rec = call(t, h, http.MethodPost, "/api/v1/auth/refresh", nil, refresh)
		require.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("logout revokes and clears cookies; refresh afterwards is 401", func(t *testing.T) {
		_, refresh := login(t, h, "alice@acme.test")

		rec := call(t, h, http.MethodPost, "/api/v1/auth/logout", nil, refresh)
		require.Equal(t, http.StatusNoContent, rec.Code)
		assert.Negative(t, cookie(t, rec, "refresh_token").MaxAge, "cookie deleted")

		rec = call(t, h, http.MethodPost, "/api/v1/auth/refresh", nil, refresh)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}
