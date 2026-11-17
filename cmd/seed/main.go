// Command seed creates a demo tenant and its owner account for local
// development. It is idempotent: an existing account is left untouched.
//
// It goes through auth.Service.Register, so the account is created exactly like
// a real sign-up (bcrypt hash, generated slug, one transaction).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"time"

	"github.com/HieuTrannn/crm-backend/internal/auth"
	"github.com/HieuTrannn/crm-backend/internal/config"
	"github.com/HieuTrannn/crm-backend/internal/platform/database"
)

const minPassword = 8 // FR-AUTH-02

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("seed failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load() // also loads .env
	if err != nil {
		return err
	}
	email := os.Getenv("SEED_OWNER_EMAIL")
	password := os.Getenv("SEED_OWNER_PASSWORD")
	fullName := envOr("SEED_OWNER_FULL_NAME", "Demo Owner")
	companyName := envOr("SEED_OWNER_COMPANY_NAME", "Demo Company")
	if _, err := mail.ParseAddress(email); err != nil {
		return errors.New("SEED_OWNER_EMAIL must be a valid email address")
	}
	if len(password) < minPassword {
		return fmt.Errorf("SEED_OWNER_PASSWORD must be at least %d characters", minPassword)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	svc, err := auth.NewService(auth.NewPostgresRepository(database.New(pool), pool),
		auth.NewTokenIssuer(cfg.JWTSecret), auth.NewMemoryLimiter(5, 3*time.Minute), auth.BcryptCost)
	if err != nil {
		return err
	}

	me, err := svc.Register(ctx, auth.RegisterInput{
		CompanyName: companyName, Email: email, Password: password, FullName: fullName,
	})
	if errors.Is(err, auth.ErrEmailTaken) {
		fmt.Printf("account %s already exists, nothing to do\n", email)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("created owner %s in tenant %q (slug %s)\n", email, me.Tenant.Name, me.Tenant.Slug)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
