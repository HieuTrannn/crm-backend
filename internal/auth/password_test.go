package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse", BcryptCost)
	require.NoError(t, err)

	assert.NotContains(t, hash, "correct horse")
	cost, err := bcrypt.Cost([]byte(hash))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, cost, 12, "FR-AUTH-02 requires bcrypt cost >= 12")

	assert.True(t, VerifyPassword(hash, "correct horse"))
	assert.False(t, VerifyPassword(hash, "correct horsE"))
	assert.False(t, VerifyPassword("not-a-bcrypt-hash", "correct horse"))

	again, err := HashPassword("correct horse", bcrypt.MinCost)
	require.NoError(t, err)
	assert.NotEqual(t, hash, again, "each hash has its own random salt")
}

func TestHashPassword_RejectsOver72Bytes(t *testing.T) {
	_, err := HashPassword(strings.Repeat("a", 73), bcrypt.MinCost)
	require.ErrorIs(t, err, ErrPasswordTooLong)
}
