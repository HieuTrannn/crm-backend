package importer

import (
	"errors"
	"net/http"

	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// Business errors of the importer module.
var (
	ErrNotFound     = errors.New("import job not found")
	ErrNotRunning   = errors.New("import job is not running")
	ErrFileTooLarge = errors.New("file is larger than 20 MB")
	ErrShuttingDown = errors.New("server is shutting down")
	ErrTooManyJobs  = errors.New("too many imports running for this tenant")
	// ErrCancelledByUser is the cancel cause of a job stopped through the API.
	ErrCancelledByUser = errors.New("cancelled by user")
)

// ErrorRules maps importer errors to HTTP responses for the httpx.Errors middleware.
func ErrorRules() []httpx.ErrorRule {
	return []httpx.ErrorRule{
		{Err: ErrNotFound, Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "import job not found"},
		{Err: ErrNotRunning, Status: http.StatusConflict, Code: "JOB_NOT_RUNNING", Message: "the import job has already finished"},
		{Err: ErrFileTooLarge, Status: http.StatusRequestEntityTooLarge, Code: "FILE_TOO_LARGE", Message: "the file must be at most 20 MB"},
		{Err: ErrShuttingDown, Status: http.StatusServiceUnavailable, Code: "SHUTTING_DOWN", Message: "the server is restarting, try again shortly"},
		{Err: ErrTooManyJobs, Status: http.StatusTooManyRequests, Code: "TOO_MANY_IMPORTS", Message: "wait for a running import to finish before starting another"},
	}
}
