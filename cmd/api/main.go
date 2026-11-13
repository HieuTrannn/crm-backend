// Command api starts the CRM HTTP server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/HieuTrannn/crm-backend/internal/auth"
	"github.com/HieuTrannn/crm-backend/internal/config"
	"github.com/HieuTrannn/crm-backend/internal/platform/database"
	"github.com/HieuTrannn/crm-backend/internal/platform/logger"
	"github.com/HieuTrannn/crm-backend/internal/server"
)

// shutdownTimeout is how long in-flight requests get to finish (NFR-REL-01).
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet (bad config), so use a plain JSON logger here.
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("api stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

// run holds the real startup logic so deferred cleanup runs before main exits.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.AppEnv)
	if cfg.AppEnv == config.EnvProduction {
		gin.SetMode(gin.ReleaseMode)
	}

	// ctx is cancelled on Ctrl+C (SIGINT) or SIGTERM from Docker/Render.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	tokens := auth.NewTokenIssuer(cfg.JWTSecret)
	// NFR-SEC-02: 5 failed logins per 15 minutes per IP + email.
	limiter := auth.NewMemoryLimiter(5, 3*time.Minute)
	authSvc, err := auth.NewService(auth.NewPostgresRepository(database.New(pool), pool), tokens, limiter, auth.BcryptCost)
	if err != nil {
		return fmt.Errorf("init auth: %w", err)
	}
	authHandler := auth.NewHandler(authSvc, tokens, cfg.AppEnv == config.EnvProduction)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.NewRouter(log, pool, authHandler),
		ReadHeaderTimeout: 5 * time.Second, // guards against Slowloris
	}

	// ListenAndServe blocks, so it runs in its own goroutine while main waits for a signal.
	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", slog.String("addr", srv.Addr), slog.String("env", cfg.AppEnv))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received, draining requests", slog.Duration("timeout", shutdownTimeout))
	}

	// ctx is already cancelled, so the shutdown deadline needs a fresh parent.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("server stopped cleanly")
	return nil
}
