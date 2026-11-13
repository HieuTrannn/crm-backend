package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the work factor for password hashes (FR-AUTH-02 requires >= 12).
// Each +1 doubles the time an attacker needs per guess.
const BcryptCost = 12

// HashPassword returns a bcrypt hash; the salt is random and stored inside the hash.
func HashPassword(password string, cost int) (string, error) {
	if len(password) > maxPasswordBytes {
		return "", ErrPasswordTooLong
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword reports whether password matches hash. bcrypt re-hashes the
// candidate with the stored salt and compares the results with
// crypto/subtle.ConstantTimeCompare, so the time taken does not reveal how many
// leading bytes matched.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// maxPasswordBytes is bcrypt's input limit; longer passwords are rejected, not truncated.
const maxPasswordBytes = 72

// ErrPasswordTooLong is returned for passwords bcrypt cannot hash.
var ErrPasswordTooLong = errors.New("password longer than 72 bytes")
