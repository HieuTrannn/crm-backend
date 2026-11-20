package httpx

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// MaxBodyBytes bounds JSON request bodies (OWASP API4). Multipart uploads are
// left to their handler, which sets its own, larger limit.
const MaxBodyBytes = 1 << 20

// Limiter decides whether one more request is allowed for a key.
type Limiter interface {
	Take(ctx context.Context, key string) (bool, error)
}

// RateLimit answers 429 RATE_LIMITED when the limiter refuses key(c).
// A nil limiter allows everything (tests, tools).
func RateLimit(l Limiter, key func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if l == nil {
			c.Next()
			return
		}
		ok, err := l.Take(c.Request.Context(), key(c))
		if err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}
		if !ok {
			WriteError(c, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests, slow down")
			return
		}
		c.Next()
	}
}

// LimitBody caps the request body at MaxBodyBytes; reading past it fails and
// BindJSON reports 400. multipart/form-data is skipped: upload handlers apply
// their own limit, which an outer smaller limit would otherwise override.
func LimitBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.ContentType(), "multipart/form-data") {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes)
		}
		c.Next()
	}
}

// SecurityHeaders sets defensive headers for a JSON API (OWASP API8). The
// Content-Security-Policy forbids everything, since responses are never HTML;
// the Swagger UI page overrides it. HSTS is only sent when served over HTTPS
// in production, where the platform terminates TLS.
func SecurityHeaders(hsts bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store") // responses carry per-user data
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

// CORS lets only allowedOrigin call the API from a browser, with cookies.
// Other origins get no CORS headers, so the browser blocks their reads.
// The frontend normally calls the API same-origin through Next.js rewrites
// (PRD 5.1); this covers direct calls, e.g. during frontend development.
func CORS(allowedOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		h := c.Writer.Header()
		h.Add("Vary", "Origin")
		if origin == "" || origin != allowedOrigin {
			c.Next()
			return
		}
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Credentials", "true")
		if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE")
			h.Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
			h.Set("Access-Control-Max-Age", "600")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
