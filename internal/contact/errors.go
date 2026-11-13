package contact

import (
	"errors"
	"net/http"

	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// Business errors of the contact module.
var (
	ErrNotFound     = errors.New("contact not found")
	ErrEmailTaken   = errors.New("contact email already exists in this tenant")
	ErrHasOpenDeals = errors.New("contact has open deals")
	ErrInvalidOwner = errors.New("owner is not a user of this tenant")
)

// ErrorRules maps contact errors to HTTP responses for the httpx.Errors middleware.
// A contact of another tenant is reported exactly like a missing one (NFR-ISO-04).
func ErrorRules() []httpx.ErrorRule {
	return []httpx.ErrorRule{
		{Err: ErrNotFound, Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "contact not found"},
		{Err: ErrEmailTaken, Status: http.StatusConflict, Code: "EMAIL_TAKEN", Message: "a contact with this email already exists"},
		{Err: ErrHasOpenDeals, Status: http.StatusConflict, Code: "CONTACT_HAS_OPEN_DEALS", Message: "contact has open deals and cannot be deleted"},
		{Err: ErrInvalidOwner, Status: http.StatusUnprocessableEntity, Code: "INVALID_OWNER", Message: "owner_id is not a user of this tenant"},
	}
}
