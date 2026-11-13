// Package contact manages a tenant's customers (FR-CON-01..04).
package contact

import (
	"time"

	"github.com/google/uuid"
)

// Contact is a customer of a tenant. Optional fields are nil (JSON null) when unset.
type Contact struct {
	ID        uuid.UUID `json:"id"`
	FullName  string    `json:"full_name"`
	Email     *string   `json:"email"`
	Phone     *string   `json:"phone"`
	Company   *string   `json:"company"`
	Notes     *string   `json:"notes"`
	OwnerID   uuid.UUID `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateInput holds a new contact. A nil OwnerID means "the caller".
type CreateInput struct {
	FullName string
	Email    *string
	Phone    *string
	Company  *string
	Notes    *string
	OwnerID  *uuid.UUID
}

// UpdateInput is a partial update: nil fields keep their value; for optional
// text fields an empty string clears the value.
type UpdateInput struct {
	FullName *string
	Email    *string
	Phone    *string
	Company  *string
	Notes    *string
	OwnerID  *uuid.UUID
}

// ListParams selects one page. Query empty means no search filter.
type ListParams struct {
	Query          string
	Limit          int
	AfterCreatedAt time.Time
	AfterID        uuid.UUID
}
