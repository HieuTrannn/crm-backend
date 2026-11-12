// Package config loads application settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
)

// Allowed values for APP_ENV.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

// minJWTSecretLen is the minimum JWT_SECRET length in bytes (256 bits for HS256).
const minJWTSecretLen = 32

// Config holds all settings read from the environment.
type Config struct {
	Port                 string
	AppEnv               string
	DatabaseURL          string
	MigrationDatabaseURL string
	JWTSecret            string
	FrontendOrigin       string
}

// Load reads and validates the environment (after loading .env, if present). It reports every problem at once
// so a misconfigured deploy can be fixed in one pass. Secret values are never
// included in error messages.
func Load() (Config, error) {
	if err := loadDotEnv(); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Port:                 getenv("PORT", "8080"),
		AppEnv:               getenv("APP_ENV", EnvDevelopment),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		MigrationDatabaseURL: os.Getenv("MIGRATION_DATABASE_URL"),
		JWTSecret:            os.Getenv("JWT_SECRET"),
		FrontendOrigin:       os.Getenv("FRONTEND_ORIGIN"),
	}

	var errs []error
	required := []struct{ name, value string }{
		{"DATABASE_URL", cfg.DatabaseURL},
		{"MIGRATION_DATABASE_URL", cfg.MigrationDatabaseURL},
		{"JWT_SECRET", cfg.JWTSecret},
		{"FRONTEND_ORIGIN", cfg.FrontendOrigin},
	}
	for _, r := range required {
		if r.value == "" {
			errs = append(errs, fmt.Errorf("%s is required", r.name))
		}
	}

	if cfg.JWTSecret != "" && len(cfg.JWTSecret) < minJWTSecretLen {
		errs = append(errs, fmt.Errorf("JWT_SECRET must be at least %d bytes", minJWTSecretLen))
	}

	switch cfg.AppEnv {
	case EnvDevelopment, EnvTest, EnvProduction:
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be one of %s, %s, %s; got %q",
			EnvDevelopment, EnvTest, EnvProduction, cfg.AppEnv))
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid config:\n%w", err)
	}
	return cfg, nil
}

// getenv returns the environment variable or fallback when it is unset or empty.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
