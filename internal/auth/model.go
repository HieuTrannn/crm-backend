// Package auth handles registration, login, token refresh/rotation, logout and
// request authentication (FR-AUTH-01..06, NFR-SEC-02/03).
package auth

import (
	"time"

	"github.com/google/uuid"
)

// Roles a user can have in a tenant.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Principal is the authenticated caller, taken from the access token.
type Principal struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}

// RegisterInput is what a new tenant owner submits.
type RegisterInput struct {
	CompanyName string
	Slug        string
	Email       string
	Password    string
	FullName    string
}

// Me is the current user together with their tenant.
type Me struct {
	ID       uuid.UUID  `json:"id"`
	Email    string     `json:"email"`
	FullName string     `json:"full_name"`
	Role     string     `json:"role"`
	Tenant   TenantInfo `json:"tenant"`
}

// TenantInfo is the public part of a tenant.
type TenantInfo struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

// Session is what login and refresh hand to the handler to put in cookies.
type Session struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshCookie    string // "<tenant_id>.<raw refresh token>"
	RefreshExpiresAt time.Time
}

// Credentials is the cross-tenant login lookup result.
type Credentials struct {
	UserID       uuid.UUID
	TenantID     uuid.UUID
	PasswordHash string
	Role         string
}

// NewUser is a user row to insert.
type NewUser struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	Email        string
	PasswordHash string
	FullName     string
	Role         string
}

// RefreshToken is a stored refresh token (only its hash is persisted).
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	FamilyID  uuid.UUID
	ExpiresAt time.Time
	RevokedAt *time.Time
	Role      string // the owning user's role, needed to issue the next access token
}
