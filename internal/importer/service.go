package importer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

// MaxFileSize is the largest accepted CSV upload (FR-IMP-01).
const MaxFileSize = 20 << 20

// finishTimeout bounds the final status update, which runs after the job's own
// context may already be cancelled.
const finishTimeout = 10 * time.Second

// Repository is the persistence the service needs (implemented in repository.go).
type Repository interface {
	// WithTenant runs fn in one transaction scoped to tenantID (RLS).
	WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(Store) error) error
}

// Store is the set of queries available inside a tenant transaction.
type Store interface {
	CreateJob(ctx context.Context, id, createdBy uuid.UUID) (Job, error)
	// GetJob returns ErrNotFound for a missing or other-tenant job.
	GetJob(ctx context.Context, id uuid.UUID) (Job, error)
	StartJob(ctx context.Context, id uuid.UUID, totalRows int32) error
	FinishJob(ctx context.Context, id uuid.UUID, status string, reason *string) error
	// InsertContacts returns the ids actually inserted; rows whose email already
	// exists are skipped.
	InsertContacts(ctx context.Context, ownerID uuid.UUID, rows []contactRow) ([]uuid.UUID, error)
	InsertErrors(ctx context.Context, jobID uuid.UUID, errs []RowError) error
	AddProgress(ctx context.Context, jobID uuid.UUID, processed, succeeded, failed int32) error
	// ListErrors returns up to limit errors with a line number above afterRow.
	ListErrors(ctx context.Context, jobID uuid.UUID, afterRow int32, limit int) ([]RowError, error)
}

// Service implements the import use cases.
type Service struct {
	repo     Repository
	jobs     *JobManager
	workers  int
	validate *validator.Validate
	log      *slog.Logger
}

// NewService wires the import service. workers is the number of validation
// goroutines per job (IMPORT_WORKERS).
func NewService(repo Repository, jobs *JobManager, workers int, log *slog.Logger) *Service {
	return &Service{repo: repo, jobs: jobs, workers: workers, validate: validator.New(), log: log}
}

// Start saves the upload to a temporary file, creates the job and returns at
// once; the import runs in the background (FR-IMP-01). src is read to the end.
func (s *Service) Start(ctx context.Context, tenantID, userID uuid.UUID, src io.Reader) (Job, error) {
	path, err := saveUpload(src)
	if err != nil {
		return Job{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		_ = os.Remove(path)
		return Job{}, fmt.Errorf("new job id: %w", err)
	}

	var job Job
	err = s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		var err error
		job, err = st.CreateJob(ctx, id, userID)
		return err
	})
	if err != nil {
		_ = os.Remove(path)
		return Job{}, fmt.Errorf("create import job: %w", err)
	}

	// The job context comes from the JobManager, not from ctx: the request
	// ends as soon as the 202 is sent, and that must not stop the import.
	err = s.jobs.Start(id, tenantID, func(jobCtx context.Context) {
		s.run(jobCtx, tenantID, userID, id, path)
	})
	if err != nil {
		_ = os.Remove(path)
		reason := err.Error()
		s.finish(ctx, tenantID, id, StatusFailed, &reason)
		return Job{}, err
	}
	return job, nil
}

// saveUpload copies src to a temporary file, refusing more than MaxFileSize.
func saveUpload(src io.Reader) (string, error) {
	f, err := os.CreateTemp("", "crm-import-*.csv")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	n, copyErr := io.Copy(f, io.LimitReader(src, MaxFileSize+1))
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil || n > MaxFileSize {
		_ = os.Remove(f.Name())
		if err == nil {
			err = ErrFileTooLarge
		}
		return "", err
	}
	return f.Name(), nil
}

// run processes one job and records how it ended. It owns the temp file.
func (s *Service) run(ctx context.Context, tenantID, userID, jobID uuid.UUID, path string) {
	defer func() { _ = os.Remove(path) }()
	status, reason := s.process(ctx, tenantID, userID, jobID, path)
	// ctx may be cancelled already; the final update must still be written.
	s.finish(context.WithoutCancel(ctx), tenantID, jobID, status, reason)
}

func (s *Service) process(ctx context.Context, tenantID, userID, jobID uuid.UUID, path string) (string, *string) {
	f, err := os.Open(path) //nolint:gosec // path is our own temp file
	if err != nil {
		return s.internalFailure(jobID, fmt.Errorf("open upload: %w", err))
	}
	defer func() { _ = f.Close() }()

	// A first streaming pass gives total_rows for the progress bar (FR-IMP-04).
	total, err := countRows(f)
	if err != nil {
		return s.internalFailure(jobID, fmt.Errorf("count rows: %w", err))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return s.internalFailure(jobID, fmt.Errorf("rewind upload: %w", err))
	}
	err = s.repo.WithTenant(ctx, tenantID, func(st Store) error { return st.StartJob(ctx, jobID, total) })
	if err != nil && ctx.Err() == nil {
		return s.internalFailure(jobID, fmt.Errorf("start job: %w", err))
	}

	if err == nil {
		err = runPipeline(ctx, f, s.workers, s.validate, s.flusher(tenantID, userID, jobID))
	}
	var herr *headerError
	switch {
	case err == nil:
		return StatusCompleted, nil
	case ctx.Err() != nil:
		reason := context.Cause(ctx).Error()
		return StatusCancelled, &reason
	case errors.As(err, &herr):
		reason := herr.Error()
		return StatusFailed, &reason
	default:
		return s.internalFailure(jobID, err)
	}
}

// flusher stores one batch in one transaction: valid rows, row errors (including
// emails that turned out to exist already) and the progress counters.
func (s *Service) flusher(tenantID, userID, jobID uuid.UUID) flushFunc {
	return func(ctx context.Context, b batch) error {
		// Validators run in parallel, so rows arrive out of order. Sorting by line
		// makes the earlier line win when two lines of a batch share an email.
		slices.SortFunc(b.rows, func(a, c contactRow) int { return cmp.Compare(a.line, c.line) })
		return s.repo.WithTenant(ctx, tenantID, func(st Store) error {
			inserted, err := st.InsertContacts(ctx, userID, b.rows)
			if err != nil {
				return err
			}
			errs := b.errs
			if len(inserted) < len(b.rows) {
				ok := make(map[uuid.UUID]bool, len(inserted))
				for _, id := range inserted {
					ok[id] = true
				}
				for _, r := range b.rows {
					if !ok[r.id] {
						errs = append(errs, RowError{RowNumber: r.line, Reason: "email already exists"})
					}
				}
			}
			if err := st.InsertErrors(ctx, jobID, errs); err != nil {
				return err
			}
			processed := int32(len(b.rows) + len(b.errs))                                        //nolint:gosec // at most batchSize
			return st.AddProgress(ctx, jobID, processed, int32(len(inserted)), int32(len(errs))) //nolint:gosec // at most batchSize
		})
	}
}

// internalFailure logs the real cause and gives the user a generic reason, so
// database details never reach the API.
func (s *Service) internalFailure(jobID uuid.UUID, err error) (string, *string) {
	s.log.Error("import job failed", slog.String("job_id", jobID.String()), slog.Any("error", err))
	reason := "internal error while importing; the rows imported so far were kept"
	return StatusFailed, &reason
}

func (s *Service) finish(ctx context.Context, tenantID, jobID uuid.UUID, status string, reason *string) {
	ctx, cancel := context.WithTimeout(ctx, finishTimeout)
	defer cancel()
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		return st.FinishJob(ctx, jobID, status, reason)
	})
	if err != nil {
		s.log.Error("cannot record import job end", slog.String("job_id", jobID.String()),
			slog.String("status", status), slog.Any("error", err))
	}
}

// Get returns one job of the tenant.
func (s *Service) Get(ctx context.Context, tenantID, id uuid.UUID) (Job, error) {
	var job Job
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		var err error
		job, err = st.GetJob(ctx, id)
		return err
	})
	if err != nil {
		return Job{}, fmt.Errorf("get import job: %w", err)
	}
	return job, nil
}

// Errors returns one page of a job's row errors, by line number, and whether
// more follow (FR-IMP-04).
func (s *Service) Errors(ctx context.Context, tenantID, id uuid.UUID, afterRow int32, limit int) ([]RowError, bool, error) {
	var rows []RowError
	err := s.repo.WithTenant(ctx, tenantID, func(st Store) error {
		if _, err := st.GetJob(ctx, id); err != nil {
			return err
		}
		var err error
		rows, err = st.ListErrors(ctx, id, afterRow, limit+1)
		return err
	})
	if err != nil {
		return nil, false, fmt.Errorf("list import errors: %w", err)
	}
	if len(rows) > limit {
		return rows[:limit], true, nil
	}
	return rows, false, nil
}

// Cancel stops a running job (FR-IMP-05). Rows already stored are kept.
func (s *Service) Cancel(ctx context.Context, tenantID, id uuid.UUID) error {
	if _, err := s.Get(ctx, tenantID, id); err != nil {
		return err
	}
	if !s.jobs.Cancel(id, ErrCancelledByUser) {
		return ErrNotRunning
	}
	return nil
}
