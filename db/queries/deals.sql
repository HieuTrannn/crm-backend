-- Every query filters by tenant_id explicitly (layer 1); RLS is layer 2.

-- Locks a live contact against soft delete until the transaction ends. Contact
-- deletion takes FOR UPDATE on the same row, so creating a deal and deleting its
-- contact cannot interleave.
-- name: LockLiveContact :one
SELECT id FROM contacts
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
FOR SHARE;

-- name: CreateDeal :one
INSERT INTO deals (id, tenant_id, contact_id, title, amount, expected_close_date, owner_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetDeal :one
SELECT * FROM deals
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- Optimistic locking (PRD 5.6): no row when the version moved on (or the deal is missing).
-- name: UpdateDeal :one
UPDATE deals
SET title = $4, amount = $5, expected_close_date = $6, owner_id = $7,
    version = version + 1, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND version = $3 AND deleted_at IS NULL
RETURNING *;

-- name: TransitionDeal :one
UPDATE deals
SET stage = sqlc.arg(stage),
    lost_reason = sqlc.narg(lost_reason),
    closed_at = CASE WHEN sqlc.arg(stage)::text IN ('won', 'lost') THEN now() END,
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id) AND version = sqlc.arg(version)
  AND deleted_at IS NULL
RETURNING *;

-- name: InsertStageHistory :exec
INSERT INTO deal_stage_history (id, tenant_id, deal_id, from_stage, to_stage, reason, changed_by)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- Optional filters are NULL when absent (FR-DEAL-05). Keyset pagination as for contacts.
-- name: ListDeals :many
SELECT * FROM deals
WHERE tenant_id = sqlc.arg(tenant_id)
  AND deleted_at IS NULL
  AND (sqlc.narg(stage)::text IS NULL OR stage = sqlc.narg(stage))
  AND (sqlc.narg(owner_id)::uuid IS NULL OR owner_id = sqlc.narg(owner_id))
  AND (sqlc.narg(contact_id)::uuid IS NULL OR contact_id = sqlc.narg(contact_id))
  AND (created_at, id) < (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- Oldest first; id breaks ties within one transaction. Uses deal_stage_history_deal_idx.
-- name: ListStageHistory :many
SELECT id, from_stage, to_stage, reason, changed_by, changed_at
FROM deal_stage_history
WHERE tenant_id = $1 AND deal_id = $2
ORDER BY changed_at, id;

-- Soft delete with optimistic locking: no row when the version moved on or the
-- deal is missing or already deleted. Stage history is kept.
-- name: SoftDeleteDeal :execrows
UPDATE deals
SET deleted_at = now(), updated_at = now(), version = version + 1
WHERE id = $1 AND tenant_id = $2 AND version = $3 AND deleted_at IS NULL;
