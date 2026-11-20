package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	issuer := NewTokenIssuer(testSecret)
	p := testPrincipal()
	valid, _, err := issuer.Issue(p, time.Now())
	require.NoError(t, err)

	var seen Principal
	r := gin.New()
	r.GET("/x", RequireAuth(issuer, nil), func(c *gin.Context) {
		seen, _ = PrincipalFrom(c.Request.Context())
		c.Status(http.StatusOK)
	})

	tests := []struct {
		name   string
		cookie string
		want   int
	}{
		{"no cookie", "", http.StatusUnauthorized},
		{"bad token", "garbage", http.StatusUnauthorized},
		{"valid token", valid, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: accessCookie, Value: tt.cookie})
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			assert.Equal(t, tt.want, rec.Code)
		})
	}
	assert.Equal(t, p, seen)
}

func TestRequireAuth_RateLimitPerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	issuer := NewTokenIssuer(testSecret)
	r := gin.New()
	r.GET("/x", RequireAuth(issuer, NewMemoryLimiter(2, time.Hour)), func(c *gin.Context) { c.Status(http.StatusOK) })
	get := func(p Principal) int {
		tok, _, err := issuer.Issue(p, time.Now())
		require.NoError(t, err)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
		req.AddCookie(&http.Cookie{Name: accessCookie, Value: tok})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	alice, bob := testPrincipal(), testPrincipal()

	assert.Equal(t, http.StatusOK, get(alice))
	assert.Equal(t, http.StatusOK, get(alice))
	assert.Equal(t, http.StatusTooManyRequests, get(alice), "third request in the window")
	assert.Equal(t, http.StatusOK, get(bob), "the limit is per user")
}
