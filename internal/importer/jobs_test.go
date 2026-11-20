package importer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestJobManager_Cancel(t *testing.T) {
	defer goleak.VerifyNone(t)
	m := NewJobManager()
	id := uuid.New()
	started := make(chan struct{})
	cause := make(chan error, 1)

	require.NoError(t, m.Start(id, uuid.New(), func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		cause <- context.Cause(ctx)
	}))
	<-started

	assert.True(t, m.Cancel(id, ErrCancelledByUser))
	assert.ErrorIs(t, <-cause, ErrCancelledByUser)
	require.NoError(t, m.Shutdown(t.Context()))
	assert.False(t, m.Cancel(id, ErrCancelledByUser), "a finished job is no longer cancellable")
	assert.False(t, m.Cancel(uuid.New(), ErrCancelledByUser), "an unknown job")
}

func TestJobManager_ShutdownWaitsForRunningJobs(t *testing.T) {
	defer goleak.VerifyNone(t)
	m := NewJobManager()
	release := make(chan struct{})
	finished := false
	require.NoError(t, m.Start(uuid.New(), uuid.New(), func(context.Context) {
		<-release
		finished = true
	}))

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	require.NoError(t, m.Shutdown(t.Context()))
	assert.True(t, finished, "Shutdown returned only after the job ended")

	err := m.Start(uuid.New(), uuid.New(), func(context.Context) {})
	require.ErrorIs(t, err, ErrShuttingDown, "no new job after shutdown began")
}

func TestJobManager_ShutdownDeadlineCancelsJobs(t *testing.T) {
	defer goleak.VerifyNone(t)
	m := NewJobManager()
	cause := make(chan error, 1)
	require.NoError(t, m.Start(uuid.New(), uuid.New(), func(ctx context.Context) {
		<-ctx.Done() // a long import that only stops when cancelled
		cause <- context.Cause(ctx)
	}))

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := m.Shutdown(ctx)

	require.True(t, errors.Is(err, context.DeadlineExceeded))
	assert.ErrorIs(t, <-cause, ErrShuttingDown, "the job learns why it was stopped")
}

func TestJobManager_LimitsJobsPerTenant(t *testing.T) {
	defer goleak.VerifyNone(t)
	m := NewJobManager()
	tenant, other := uuid.New(), uuid.New()
	release := make(chan struct{})
	block := func(context.Context) { <-release }

	for range MaxJobsPerTenant {
		require.NoError(t, m.Start(uuid.New(), tenant, block))
	}
	require.ErrorIs(t, m.Start(uuid.New(), tenant, block), ErrTooManyJobs)
	require.NoError(t, m.Start(uuid.New(), other, block), "another tenant is not affected")

	close(release)
	require.NoError(t, m.Shutdown(t.Context()))
}
