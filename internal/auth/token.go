package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Token lifetimes (FR-AUTH-03).
const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 7 * 24 * time.Hour
)

const refreshTokenBytes = 32

var errInvalidAccessToken = errors.New("invalid access token")

// accessClaims is the JWT payload: sub (user id), tid, role, exp, iat.
type accessClaims struct {
	TenantID string `json:"tid"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// TokenIssuer signs and verifies access tokens with HS256.
type TokenIssuer struct {
	secret []byte
}

// NewTokenIssuer creates an issuer; config already enforces a 32+ byte secret.
func NewTokenIssuer(secret string) *TokenIssuer {
	return &TokenIssuer{secret: []byte(secret)}
}

// Issue returns a signed access token for p and its expiry.
func (t *TokenIssuer) Issue(p Principal, now time.Time) (string, time.Time, error) {
	exp := now.Add(AccessTokenTTL)
	claims := accessClaims{
		TenantID: p.TenantID.String(),
		Role:     p.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   p.UserID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, exp, nil
}

// Parse verifies signature, algorithm and expiry, then returns the principal.
// Pinning HS256 blocks "alg: none" and algorithm-confusion tokens.
func (t *TokenIssuer) Parse(token string) (Principal, error) {
	var claims accessClaims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %w", errInvalidAccessToken, err)
	}
	userID, errU := uuid.Parse(claims.Subject)
	tenantID, errT := uuid.Parse(claims.TenantID)
	if errU != nil || errT != nil || (claims.Role != RoleOwner && claims.Role != RoleMember) {
		return Principal{}, errInvalidAccessToken
	}
	return Principal{UserID: userID, TenantID: tenantID, Role: claims.Role}, nil
}

// newRefreshToken returns 32 random bytes from crypto/rand, base64url encoded.
// math/rand would be predictable and must never be used for secrets.
func newRefreshToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashRefreshToken is what the database stores: a stolen database dump then
// contains no usable tokens. SHA-256 (not bcrypt) is enough because the input
// is 256 random bits, not a guessable password.
func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// tokenHashEqual compares two token hashes in constant time, so response timing
// does not leak how many leading characters of a guess were right.
func tokenHashEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// refreshCookieValue packs the tenant with the token: refresh happens when the
// access token may be expired, and the tenant is needed to open a tenant-scoped
// (RLS) transaction. Tampering with the tenant part only yields "not found".
func refreshCookieValue(tenantID uuid.UUID, raw string) string {
	return tenantID.String() + "." + raw
}

func parseRefreshCookie(v string) (uuid.UUID, string, error) {
	tenantPart, raw, ok := strings.Cut(v, ".")
	if !ok || raw == "" {
		return uuid.Nil, "", ErrInvalidRefreshToken
	}
	tenantID, err := uuid.Parse(tenantPart)
	if err != nil || tenantID == uuid.Nil {
		return uuid.Nil, "", ErrInvalidRefreshToken
	}
	return tenantID, raw, nil
}
