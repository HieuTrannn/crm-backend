package httpx

import (
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HeaderRequestID is the header used to receive and return the request id.
const HeaderRequestID = "X-Request-ID"

const ctxKeyRequestID = "request_id"

// validRequestID limits client-supplied ids so they cannot inject junk into logs.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestID reuses a well-formed X-Request-ID from the client or generates a
// UUID v7, then stores it in the gin context and echoes it in the response.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(HeaderRequestID)
		if !validRequestID.MatchString(id) {
			id = newID()
		}
		c.Set(ctxKeyRequestID, id)
		c.Header(HeaderRequestID, id)
		c.Next()
	}
}

// RequestIDFrom returns the request id set by the RequestID middleware.
func RequestIDFrom(c *gin.Context) string {
	return c.GetString(ctxKeyRequestID)
}

// Logger writes one structured log line per request after it completes.
func Logger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		log.LogAttrs(c.Request.Context(), level, "http request",
			slog.String("request_id", RequestIDFrom(c)),
			slog.String("method", c.Request.Method),
			// Path only, never the raw query string: it could carry tokens.
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", status),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
		)
	}
}

// Recover turns a panic into a logged 500 with the standard error body.
func Recover(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		log.ErrorContext(c.Request.Context(), "panic recovered",
			slog.String("request_id", RequestIDFrom(c)),
			slog.Any("panic", recovered),
		)
		WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	})
}

func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// NewV7 only fails if the OS random source fails; v4 is a safe fallback.
		return uuid.NewString()
	}
	return id.String()
}
