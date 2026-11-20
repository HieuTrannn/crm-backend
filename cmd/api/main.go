// Command api starts the CRM HTTP server.
package main

import (
	"context"
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
	"github.com/HieuTrannn/crm-backend/internal/contact"
	"github.com/HieuTrannn/crm-backend/internal/dashboard"
	"github.com/HieuTrannn/crm-backend/internal/deal"
	"github.com/HieuTrannn/crm-backend/internal/importer"
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
	loginLimiter := auth.NewMemoryLimiter(5, 3*time.Minute)
	// NFR-SEC-02: 100 requests per minute per user, on every authenticated route.
	apiLimiter := auth.NewMemoryLimiter(100, 600*time.Millisecond)
	// OWASP API6: 10 sign-ups per hour per IP.
	registerLimiter := auth.NewMemoryLimiter(10, 6*time.Minute)
	authSvc, err := auth.NewService(auth.NewPostgresRepository(database.New(pool), pool), tokens, loginLimiter, auth.BcryptCost)
	if err != nil {
		return fmt.Errorf("init auth: %w", err)
	}
	requireAuth := auth.RequireAuth(tokens, apiLimiter)
	production := cfg.AppEnv == config.EnvProduction
	authHandler := auth.NewHandler(authSvc, requireAuth, registerLimiter, production)

	db := database.New(pool)
	contactHandler := contact.NewHandler(contact.NewService(contact.NewPostgresRepository(db)), requireAuth)
	dealHandler := deal.NewHandler(deal.NewService(deal.NewPostgresRepository(db)), requireAuth)
	dashboardHandler := dashboard.NewHandler(dashboard.NewService(dashboard.NewPostgresRepository(db)), requireAuth)
	importJobs := importer.NewJobManager()
	importHandler := importer.NewHandler(
		importer.NewService(importer.NewPostgresRepository(db), importJobs, cfg.ImportWorkers, log), requireAuth)

	router := server.NewRouter(log, pool, server.Options{FrontendOrigin: cfg.FrontendOrigin, HSTS: production},
		authHandler, contactHandler, dealHandler, dashboardHandler, importHandler)
	docs := cfg.AppEnv == config.EnvDevelopment
	if docs {
		// Path is relative to the repo root, where `go run ./cmd/api` is started.
		server.MountDocs(router, "api/openapi.yaml")
	}

	listeners := []server.Listener{{Server: newHTTPServer(cfg.Port, router)}}
	urls := []string{"http://localhost:" + cfg.Port}
	if cfg.HTTPSEnabled() {
		listeners = append(listeners, server.Listener{
			Server: newHTTPServer(cfg.HTTPSPort, router), CertFile: cfg.TLSCertFile, KeyFile: cfg.TLSKeyFile,
		})
		urls = append(urls, "https://localhost:"+cfg.HTTPSPort)
	}
	// Logged last so it is not buried under gin's route listing.
	for _, u := range urls {
		log.Info("listening on "+u, slog.String("env", cfg.AppEnv))
		if docs {
			log.Info("swagger ui: " + u + "/docs")
		}
	}

	serveErr := server.Serve(ctx, log, shutdownTimeout, listeners...)

	// No request can start an import any more. Give running imports the same
	// window to finish, then cancel them; the deferred pool.Close runs after.
	jobsCtx, cancelJobs := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelJobs()
	if err := importJobs.Shutdown(jobsCtx); err != nil {
		log.Warn("import jobs cancelled at shutdown", slog.Any("error", err))
	}

	if serveErr != nil {
		return serveErr
	}
	log.Info("server stopped cleanly")
	return nil
}

func newHTTPServer(port string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + port,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second, // guards against Slowloris
		// OWASP API4: bound slow clients. ReadTimeout covers a 20 MB upload at ~1.5 Mbit/s.
		ReadTimeout: 2 * time.Minute,
		IdleTimeout: 2 * time.Minute,
	}
}
