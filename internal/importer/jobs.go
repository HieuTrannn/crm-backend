package importer

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// JobManager runs import jobs in background goroutines, each with its own
// context derived from the manager rather than from the HTTP request, so a job
// outlives the request that started it. It can cancel one job, and it lets
// graceful shutdown wait for running jobs or cancel them.
//
// shortcut: jobs live in this process only. A crash leaves a job "running" in the
// database forever, and with several API instances a cancel must reach the one
// running the job. Move to a DB-backed queue (claimed rows + heartbeat) before
// running more than one instance.
type JobManager struct {
	base    context.Context
	stopAll context.CancelCauseFunc

	mu        sync.Mutex
	cancels   map[uuid.UUID]context.CancelCauseFunc
	perTenant map[uuid.UUID]int // running jobs per tenant
	closed    bool
	running   sync.WaitGroup
}

// NewJobManager creates an empty manager.
func NewJobManager() *JobManager {
	base, stopAll := context.WithCancelCause(context.Background())
	return &JobManager{base: base, stopAll: stopAll,
		cancels: map[uuid.UUID]context.CancelCauseFunc{}, perTenant: map[uuid.UUID]int{}}
}

// MaxJobsPerTenant bounds concurrent imports of one tenant (OWASP API4), so one
// tenant cannot use up the CPUs and database connections of everyone else.
const MaxJobsPerTenant = 2

// Start runs fn in a new goroutine. fn must return soon after its context is
// cancelled. After Shutdown has begun, Start refuses with ErrShuttingDown; when
// the tenant already runs MaxJobsPerTenant jobs, with ErrTooManyJobs.
func (m *JobManager) Start(id, tenantID uuid.UUID, fn func(ctx context.Context)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrShuttingDown
	}
	if m.perTenant[tenantID] >= MaxJobsPerTenant {
		return ErrTooManyJobs
	}
	ctx, cancel := context.WithCancelCause(m.base)
	m.cancels[id] = cancel
	m.perTenant[tenantID]++
	m.running.Add(1)
	go func() {
		defer m.running.Done()
		defer func() {
			m.mu.Lock()
			delete(m.cancels, id)
			if m.perTenant[tenantID]--; m.perTenant[tenantID] == 0 {
				delete(m.perTenant, tenantID)
			}
			m.mu.Unlock()
			cancel(nil)
		}()
		fn(ctx)
	}()
	return nil
}

// Cancel stops a running job with the given cause. It reports false when the
// job is not running in this process (finished, or never started here).
func (m *JobManager) Cancel(id uuid.UUID, cause error) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel, ok := m.cancels[id]
	if ok {
		cancel(cause)
	}
	return ok
}

// Shutdown stops accepting jobs and waits for running ones until ctx is done;
// then it cancels them all (cause ErrShuttingDown) and waits for them to return.
func (m *JobManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.running.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		m.stopAll(ErrShuttingDown)
		<-done // jobs return promptly once cancelled
		return ctx.Err()
	}
}
