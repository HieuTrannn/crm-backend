// Package importer imports contacts from CSV files in the background
// (FR-IMP-01..05): upload, a reader -> validators -> batch writer pipeline,
// progress, row errors and cancellation.
package importer

import (
	"time"

	"github.com/google/uuid"
)

// Job statuses.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Job is one CSV import and its progress.
type Job struct {
	ID            uuid.UUID  `json:"id"`
	Status        string     `json:"status"`
	TotalRows     int32      `json:"total_rows"`
	ProcessedRows int32      `json:"processed_rows"`
	SuccessRows   int32      `json:"success_rows"`
	ErrorRows     int32      `json:"error_rows"`
	FailureReason *string    `json:"failure_reason"`
	CreatedAt     time.Time  `json:"created_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

// RowError is a rejected CSV line. RowNumber is the line in the file (header = 1).
type RowError struct {
	RowNumber int32  `json:"row_number"`
	Reason    string `json:"reason"`
}

// contactRow is a validated line, ready to insert.
type contactRow struct {
	line     int32
	id       uuid.UUID
	fullName string
	email    string
	phone    string
	company  string
	notes    string
}

// batch is what the writer stores in one transaction.
type batch struct {
	rows []contactRow
	errs []RowError
}
