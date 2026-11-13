package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret-at-least-32-bytes-long!!"

func testPrincipal() Principal {
	return Principal{UserID: uuid.Must(uuid.NewV7()), TenantID: uuid.Must(uuid.NewV7()), Role: RoleOwner}
}

func TestAccessToken_RoundTrip(t *testing.T) {
	issuer := NewTokenIssuer(testSecret)
	p := testPrincipal()

	token, exp, err := issuer.Issue(p, time.Now())
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), exp, time.Second)

	got, err := issuer.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, p, got)
}

func TestAccessToken_Rejected(t *testing.T) {
	issuer := NewTokenIssuer(testSecret)
	p := testPrincipal()
	claims := accessClaims{
		TenantID: p.TenantID.String(), Role: p.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   p.UserID.String(),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
	sign := func(m jwt.SigningMethod, key any, c jwt.Claims) string {
		s, err := jwt.NewWithClaims(m, c).SignedString(key)
		require.NoError(t, err)
		return s
	}
	expired := claims
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	valid, _, err := issuer.Issue(p, time.Now())
	require.NoError(t, err)

	tests := map[string]string{
		"expired":          sign(jwt.SigningMethodHS256, []byte(testSecret), expired),
		"wrong secret":     sign(jwt.SigningMethodHS256, []byte("another-secret-also-32-bytes-long!!"), claims),
		"other algorithm":  sign(jwt.SigningMethodHS512, []byte(testSecret), claims),
		"alg none":         sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, claims),
		"tampered payload": valid[:strings.LastIndex(valid, ".")-2] + "xx" + valid[strings.LastIndex(valid, "."):],
		"garbage":          "not-a-jwt",
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := issuer.Parse(token)
			require.Error(t, err)
		})
	}
}

func TestRefreshToken_RandomAndHashed(t *testing.T) {
	a, err := newRefreshToken()
	require.NoError(t, err)
	b, err := newRefreshToken()
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
	assert.Len(t, a, 43, "32 bytes base64url without padding")
	assert.Len(t, hashRefreshToken(a), 64, "hex SHA-256")
	assert.NotContains(t, hashRefreshToken(a), a)
	assert.True(t, tokenHashEqual(hashRefreshToken(a), hashRefreshToken(a)))
	assert.False(t, tokenHashEqual(hashRefreshToken(a), hashRefreshToken(b)))
}

func TestParseRefreshCookie(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	gotTenant, raw, err := parseRefreshCookie(refreshCookieValue(tenantID, "abc_-123"))
	require.NoError(t, err)
	assert.Equal(t, tenantID, gotTenant)
	assert.Equal(t, "abc_-123", raw)

	for _, bad := range []string{"", "abc", "not-a-uuid.abc", tenantID.String() + ".", uuid.Nil.String() + ".abc"} {
		_, _, err := parseRefreshCookie(bad)
		assert.ErrorIs(t, err, ErrInvalidRefreshToken, bad)
	}
}
