-- Every query filters by tenant_id explicitly (layer 1); RLS is layer 2.

-- name: CreateImportJob :one
INSERT INTO import_jobs (id, tenant_id, created_by)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetImportJob :one
SELECT * FROM import_jobs
WHERE id = $1 AND tenant_id = $2;

-- name: StartImportJob :exec
UPDATE import_jobs
SET status = 'running', total_rows = $3
WHERE id = $1 AND tenant_id = $2;

-- Only a job that has not ended yet can be finished.
-- name: FinishImportJob :exec
UPDATE import_jobs
SET status = sqlc.arg(status), failure_reason = sqlc.narg(failure_reason), finished_at = now()
WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id) AND status IN ('pending', 'running');

-- Progress is updated once per batch, in the batch's transaction.
-- name: AddImportProgress :exec
UPDATE import_jobs
SET processed_rows = processed_rows + sqlc.arg(processed),
    success_rows = success_rows + sqlc.arg(succeeded),
    error_rows = error_rows + sqlc.arg(failed)
WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id);

-- Batch insert (FR-IMP-02). COPY cannot be used: PostgreSQL rejects COPY FROM
-- into a table with row-level security. Several unnest() in one SELECT list are
-- zipped row by row (PostgreSQL 10+). Empty strings become NULL. A row whose
-- email already exists among live contacts (or earlier in the same batch) is
-- skipped and its id is missing from the result.
-- name: InsertImportedContacts :many
INSERT INTO contacts (id, tenant_id, full_name, email, phone, company, notes, owner_id)
SELECT u.id, sqlc.arg(tenant_id), u.full_name, NULLIF(u.email, ''), NULLIF(u.phone, ''),
       NULLIF(u.company, ''), NULLIF(u.notes, ''), sqlc.arg(owner_id)
FROM (SELECT unnest(sqlc.arg(ids)::uuid[])         AS id,
             unnest(sqlc.arg(full_names)::text[])  AS full_name,
             unnest(sqlc.arg(emails)::text[])      AS email,
             unnest(sqlc.arg(phones)::text[])      AS phone,
             unnest(sqlc.arg(companies)::text[])   AS company,
             unnest(sqlc.arg(notes)::text[])       AS notes) AS u
ON CONFLICT (tenant_id, email) WHERE deleted_at IS NULL DO NOTHING
RETURNING id;

-- name: InsertImportErrors :exec
INSERT INTO import_job_errors (job_id, tenant_id, row_number, reason)
SELECT sqlc.arg(job_id), sqlc.arg(tenant_id), u.row_number, u.reason
FROM (SELECT unnest(sqlc.arg(row_numbers)::int[]) AS row_number,
             unnest(sqlc.arg(reasons)::text[])    AS reason) AS u;

-- Keyset on the line number (FR-IMP-04).
-- name: ListImportErrors :many
SELECT row_number, reason FROM import_job_errors
WHERE job_id = sqlc.arg(job_id) AND tenant_id = sqlc.arg(tenant_id) AND row_number > sqlc.arg(after_row)
ORDER BY row_number
LIMIT sqlc.arg(row_limit);
