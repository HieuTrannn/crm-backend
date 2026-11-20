package auth

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// principalKey is an unexported type, so no other package can read or
// overwrite the principal by accident (a plain string key could collide).
type principalKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the authenticated caller set by RequireAuth.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// CurrentPrincipal returns the caller for a handler behind RequireAuth. If the
// route was mounted without RequireAuth it writes a 401 and returns false.
func CurrentPrincipal(c *gin.Context) (Principal, bool) {
	p, ok := PrincipalFrom(c.Request.Context())
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
	}
	return p, ok
}

// RequireAuth verifies the access_token cookie and stores the Principal in the
// request context. Missing or invalid token: 401 (FR-AUTH-06). It then counts
// the request against the caller's rate limit (NFR-SEC-02): over the limit is
// 429. The limit is per user, so it can only be applied once the user is known.
// A nil limiter disables the limit.
func RequireAuth(tokens *TokenIssuer, limiter httpx.Limiter) gin.HandlerFunc {
	limit := httpx.RateLimit(limiter, func(c *gin.Context) string {
		p, _ := PrincipalFrom(c.Request.Context())
		return "user:" + p.UserID.String()
	})
	return func(c *gin.Context) {
		raw, err := c.Cookie(accessCookie)
		if err != nil {
			httpx.WriteError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		p, err := tokens.Parse(raw)
		if err != nil {
			httpx.WriteError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		c.Request = c.Request.WithContext(WithPrincipal(c.Request.Context(), p))
		limit(c) // calls c.Next() when allowed, aborts with 429 otherwise
	}
}
