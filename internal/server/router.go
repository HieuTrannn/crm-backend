// Package server wires middleware and module routes into one gin engine.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"

	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

const readyTimeout = 2 * time.Second

// Pinger is what /readyz needs from the database. *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Module is a feature that mounts routes under /api/v1 and declares how its
// business errors map to HTTP responses.
type Module interface {
	RegisterRoutes(api *gin.RouterGroup)
	ErrorRules() []httpx.ErrorRule
}

// NewRouter builds the gin engine with shared middleware, system routes and modules.
func NewRouter(log *slog.Logger, db Pinger, modules ...Module) *gin.Engine {
	useJSONFieldNames()

	r := gin.New()
	// shortcut: ClientIP is the TCP peer; behind Render/Next.js every user shares the proxy IP,
	// so login rate limiting degrades to per-email. Configure trusted proxy ranges when deploying.
	_ = r.SetTrustedProxies(nil)

	var rules []httpx.ErrorRule
	for _, m := range modules {
		rules = append(rules, m.ErrorRules()...)
	}
	// Recover sits inside Logger so the log line records the final 500 status;
	// Errors sits innermost so it sees what handlers attached with c.Error.
	r.Use(httpx.RequestID(), httpx.Logger(log), httpx.Recover(log), httpx.Errors(log, rules...))

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readyTimeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			log.WarnContext(ctx, "readiness check failed",
				slog.String("request_id", httpx.RequestIDFrom(c)),
				slog.Any("error", err),
			)
			// Details stay in the log; the public response does not describe the DB.
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	for _, m := range modules {
		m.RegisterRoutes(api)
	}
	return r
}

// useJSONFieldNames makes validation errors name fields by their JSON name
// (full_name) instead of the Go name (FullName). gin's validator is a library
// singleton, so this configures it once per router build.
func useJSONFieldNames() {
	v, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return
	}
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return f.Name
		}
		return name
	})
}
