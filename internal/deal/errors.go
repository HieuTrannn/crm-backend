package deal

import (
	"errors"
	"net/http"

	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// Business errors of the deal module.
var (
	ErrNotFound          = errors.New("deal not found")
	ErrVersionConflict   = errors.New("deal was modified by someone else")
	ErrInvalidTransition = errors.New("stage transition not allowed")
	ErrReasonRequired    = errors.New("a reason is required for this transition")
	ErrInvalidContact    = errors.New("contact does not exist in this tenant")
	ErrInvalidOwner      = errors.New("owner is not a user of this tenant")
)

// VersionConflictError carries the latest deal so the client can merge and retry
// (FR-DEAL-04). errors.Is(err, ErrVersionConflict) is true for it.
type VersionConflictError struct {
	Latest Deal
}

func (e *VersionConflictError) Error() string { return ErrVersionConflict.Error() }

// Unwrap lets errors.Is match ErrVersionConflict.
func (e *VersionConflictError) Unwrap() error { return ErrVersionConflict }

// ErrorDetails implements httpx.Detailer: the response's details is the latest deal.
func (e *VersionConflictError) ErrorDetails() any { return e.Latest }

// ErrorRules maps deal errors to HTTP responses for the httpx.Errors middleware.
func ErrorRules() []httpx.ErrorRule {
	return []httpx.ErrorRule{
		{Err: ErrNotFound, Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "deal not found"},
		{Err: ErrVersionConflict, Status: http.StatusConflict, Code: "VERSION_CONFLICT",
			Message: "the deal was modified by someone else; details holds the latest version"},
		{Err: ErrInvalidTransition, Status: http.StatusUnprocessableEntity, Code: "INVALID_TRANSITION",
			Message: "this stage transition is not allowed"},
		{Err: ErrReasonRequired, Status: http.StatusUnprocessableEntity, Code: "REASON_REQUIRED",
			Message: "lost_reason is required to move to lost; reopen_reason to reopen a lost deal"},
		{Err: ErrInvalidContact, Status: http.StatusUnprocessableEntity, Code: "INVALID_CONTACT",
			Message: "contact_id is not a contact of this tenant"},
		{Err: ErrInvalidOwner, Status: http.StatusUnprocessableEntity, Code: "INVALID_OWNER",
			Message: "owner_id is not a user of this tenant"},
	}
}
