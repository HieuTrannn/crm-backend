package config

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/joho/godotenv"
)

// loadDotEnv loads .env from the working directory for local development.
// A missing file is fine (production sets real environment variables), and
// variables that are already set are never overridden, so the real
// environment always wins over .env.
func loadDotEnv() error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	return nil
}
