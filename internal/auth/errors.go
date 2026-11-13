package auth

import (
	"errors"
	"net/http"

	"github.com/HieuTrannn/crm-backend/internal/platform/httpx"
)

// Business errors of the auth module.
var (
	ErrEmailTaken          = errors.New("email already registered")
	ErrSlugTaken           = errors.New("tenant slug already taken")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	ErrRefreshTokenReused  = errors.New("refresh token reused")
	ErrTooManyAttempts     = errors.New("too many login attempts")
	ErrNotFound            = errors.New("not found")
)

// ErrorRules maps auth errors to HTTP responses for the httpx.Errors middleware.
// Reuse is reported like any invalid token so a thief learns nothing extra.
func ErrorRules() []httpx.ErrorRule {
	return []httpx.ErrorRule{
		{Err: ErrEmailTaken, Status: http.StatusConflict, Code: "EMAIL_TAKEN", Message: "email already registered"},
		{Err: ErrSlugTaken, Status: http.StatusConflict, Code: "SLUG_TAKEN", Message: "tenant slug already taken"},
		{Err: ErrInvalidCredentials, Status: http.StatusUnauthorized, Code: "INVALID_CREDENTIALS", Message: "invalid email or password"},
		{Err: ErrInvalidRefreshToken, Status: http.StatusUnauthorized, Code: "INVALID_REFRESH_TOKEN", Message: "invalid or expired refresh token"},
		{Err: ErrRefreshTokenReused, Status: http.StatusUnauthorized, Code: "INVALID_REFRESH_TOKEN", Message: "invalid or expired refresh token"},
		{Err: ErrTooManyAttempts, Status: http.StatusTooManyRequests, Code: "TOO_MANY_ATTEMPTS", Message: "too many login attempts, try again later"},
		{Err: ErrPasswordTooLong, Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "password must be at most 72 bytes"},
		{Err: ErrNotFound, Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "resource not found"},
	}
}
