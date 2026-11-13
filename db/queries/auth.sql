-- Every query filters by tenant_id explicitly (layer 1); RLS is layer 2.

-- name: CreateTenant :exec
INSERT INTO tenants (id, name, slug)
VALUES ($1, $2, $3);

-- name: CreateUser :exec
INSERT INTO users (id, tenant_id, email, password_hash, full_name, role)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetUserWithTenant :one
SELECT u.id, u.tenant_id, u.email, u.full_name, u.role,
       t.name AS tenant_name, t.slug AS tenant_slug
FROM users u
JOIN tenants t ON t.id = u.tenant_id
WHERE u.id = $1 AND u.tenant_id = $2;

-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (id, user_id, tenant_id, token_hash, family_id, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetRefreshTokenByHash :one
SELECT rt.id, rt.user_id, rt.token_hash, rt.family_id, rt.expires_at, rt.revoked_at, u.role
FROM refresh_tokens rt
JOIN users u ON u.id = rt.user_id AND u.tenant_id = rt.tenant_id
WHERE rt.token_hash = $1 AND rt.tenant_id = $2;

-- Revokes one token only if it is still active; 0 rows means someone already used it.
-- name: RevokeRefreshToken :execrows
UPDATE refresh_tokens
SET revoked_at = now()
WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL;

-- name: RevokeRefreshTokenFamily :exec
UPDATE refresh_tokens
SET revoked_at = now()
WHERE family_id = $1 AND tenant_id = $2 AND revoked_at IS NULL;
