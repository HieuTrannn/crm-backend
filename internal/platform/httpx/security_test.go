package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countLimiter struct{ left int }

func (l *countLimiter) Take(context.Context, string) (bool, error) {
	l.left--
	return l.left >= 0, nil
}

func serve(t *testing.T, r *gin.Engine, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", RateLimit(&countLimiter{left: 2}, func(*gin.Context) string { return "k" }), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	assert.Equal(t, http.StatusOK, serve(t, r, http.MethodGet, "/x", nil, nil).Code)
	assert.Equal(t, http.StatusOK, serve(t, r, http.MethodGet, "/x", nil, nil).Code)
	rec := serve(t, r, http.MethodGet, "/x", nil, nil)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Contains(t, rec.Body.String(), "RATE_LIMITED")
}

func TestLimitBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(LimitBody())
	r.POST("/x", func(c *gin.Context) {
		var v map[string]any
		if BindJSON(c, &v) {
			c.Status(http.StatusOK)
		}
	})
	big := `{"a":"` + strings.Repeat("x", MaxBodyBytes) + `"}`

	assert.Equal(t, http.StatusOK, serve(t, r, http.MethodPost, "/x", strings.NewReader(`{"a":1}`),
		map[string]string{"Content-Type": "application/json"}).Code)
	assert.Equal(t, http.StatusBadRequest, serve(t, r, http.MethodPost, "/x", strings.NewReader(big),
		map[string]string{"Content-Type": "application/json"}).Code, "over 1 MB is rejected")
}

func TestSecurityHeadersAndCORS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders(true), CORS("https://app.example"))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := serve(t, r, http.MethodGet, "/x", nil, map[string]string{"Origin": "https://app.example"})
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "default-src 'none'")
	assert.NotEmpty(t, rec.Header().Get("Strict-Transport-Security"))
	assert.Equal(t, "https://app.example", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))

	rec = serve(t, r, http.MethodGet, "/x", nil, map[string]string{"Origin": "https://evil.example"})
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), "other origins get no CORS grant")

	rec = serve(t, r, http.MethodOptions, "/x", nil, map[string]string{
		"Origin": "https://app.example", "Access-Control-Request-Method": "PATCH",
	})
	assert.Equal(t, http.StatusNoContent, rec.Code, "preflight answered")
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "PATCH")
}
