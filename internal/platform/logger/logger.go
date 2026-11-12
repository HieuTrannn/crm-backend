// Package logger builds the application's structured JSON logger.
package logger

import (
	"log/slog"
	"os"
)

// New returns a JSON logger writing to stdout. Development logs at Debug,
// every other environment at Info.
func New(appEnv string) *slog.Logger {
	level := slog.LevelInfo
	if appEnv == "development" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
