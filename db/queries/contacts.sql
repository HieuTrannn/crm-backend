-- Every query filters by tenant_id explicitly (layer 1); RLS is layer 2.

-- name: CreateContact :one
INSERT INTO contacts (id, tenant_id, full_name, email, phone, company, notes, owner_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetContact :one
SELECT *
FROM contacts
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- Locks the row for a read-modify-write update in the same transaction.
-- name: GetContactForUpdate :one
SELECT *
FROM contacts
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
FOR UPDATE;

-- name: UpdateContact :one
UPDATE contacts
SET full_name = $3, email = $4, phone = $5, company = $6, notes = $7, owner_id = $8, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteContact :execrows
UPDATE contacts
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- Keyset pagination: rows strictly after the cursor (created_at, id), newest first.
-- The first page passes a cursor beyond any real row. Uses contacts_tenant_created_idx.
-- name: ListContacts :many
SELECT *
FROM contacts
WHERE tenant_id = sqlc.arg(tenant_id)
  AND deleted_at IS NULL
  AND (created_at, id) < (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- Same as ListContacts plus a substring match on search_text (already lower-cased and
-- unaccented). query must be LIKE-escaped by the caller; SQL normalizes it the same way.
-- name: SearchContacts :many
SELECT *
FROM contacts
WHERE tenant_id = sqlc.arg(tenant_id)
  AND deleted_at IS NULL
  AND search_text LIKE '%' || immutable_unaccent(lower(sqlc.arg(query)::text)) || '%'
  AND (created_at, id) < (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- Open = not won and not lost. Runs in the soft-delete transaction after the
-- contact row is locked FOR UPDATE (FR-CON-04).
-- name: ContactHasOpenDeals :one
SELECT EXISTS (
    SELECT 1 FROM deals
    WHERE tenant_id = $1 AND contact_id = $2 AND stage NOT IN ('won', 'lost') AND deleted_at IS NULL
);
