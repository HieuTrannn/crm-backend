// Package server wires middleware and module routes into one gin engine.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

const readyTimeout = 2 * time.Second

// Pinger is what /readyz needs from the database. *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// NewRouter builds the gin engine with shared middleware and system routes.
func NewRouter(log *slog.Logger, db Pinger) *gin.Engine {
	r := gin.New()
	// Recover sits inside Logger so the log line records the final 500 status.
	r.Use(httpx.RequestID(), httpx.Logger(log), httpx.Recover(log))

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

	return r
}
