package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Listener is one HTTP server to run. Empty CertFile/KeyFile means plain HTTP.
type Listener struct {
	Server   *http.Server
	CertFile string
	KeyFile  string
}

// Serve starts every listener, then blocks until ctx is cancelled (SIGINT/SIGTERM)
// or one listener fails. It then shuts all of them down, giving in-flight
// requests up to shutdownTimeout to finish (NFR-REL-01).
func Serve(ctx context.Context, log *slog.Logger, shutdownTimeout time.Duration, listeners ...Listener) error {
	// Buffered so a failing goroutine never blocks, even if nobody reads anymore.
	errCh := make(chan error, len(listeners))
	for _, l := range listeners {
		go func() {
			var err error
			if l.CertFile != "" {
				err = l.Server.ListenAndServeTLS(l.CertFile, l.KeyFile)
			} else {
				err = l.Server.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("listen %s: %w", l.Server.Addr, err)
			}
		}()
	}

	var runErr error
	select {
	case runErr = <-errCh:
	case <-ctx.Done():
		log.Info("shutdown signal received, draining requests", slog.Duration("timeout", shutdownTimeout))
	}

	// ctx may already be cancelled, so the shutdown deadline needs a fresh parent.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	errs := []error{runErr}
	for _, l := range listeners {
		if err := l.Server.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown %s: %w", l.Server.Addr, err))
		}
	}
	return errors.Join(errs...)
}
