-- Runs as app_user inside a tenant transaction, like the API. The statements are
-- the exact SQL sqlc generated (db/sqlc/contacts.sql.go), prepared with parameters.
\set tenant_a '01900000-0000-7000-8000-00000000000a'
\pset pager off

BEGIN;
SELECT set_config('app.tenant_id', :'tenant_a', true) \g /dev/null

PREPARE list_contacts(uuid, timestamptz, uuid, int) AS
SELECT id, tenant_id, full_name, email, phone, company, notes, owner_id, created_at, updated_at, deleted_at, search_text
FROM contacts
WHERE tenant_id = $1
  AND deleted_at IS NULL
  AND (created_at, id) < ($2::timestamptz, $3::uuid)
ORDER BY created_at DESC, id DESC
LIMIT $4;

PREPARE search_contacts(uuid, text, timestamptz, uuid, int) AS
SELECT id, tenant_id, full_name, email, phone, company, notes, owner_id, created_at, updated_at, deleted_at, search_text
FROM contacts
WHERE tenant_id = $1
  AND deleted_at IS NULL
  AND search_text LIKE '%' || immutable_unaccent(lower($2::text)) || '%'
  AND (created_at, id) < ($3::timestamptz, $4::uuid)
ORDER BY created_at DESC, id DESC
LIMIT $5;

-- Warm the cache so timings reflect a running server, not the first disk read.
SELECT count(*) FROM contacts \g /dev/null

SELECT count(*) AS live_contacts_of_a FROM contacts WHERE deleted_at IS NULL;
SELECT count(*) AS nguyen_matches FROM contacts
WHERE deleted_at IS NULL AND immutable_unaccent(lower(full_name)) LIKE '%nguyen%';

-- Cursor of the 50,001st row, to show a deep page.
SELECT created_at AS deep_ts, id AS deep_id FROM contacts WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC OFFSET 50000 LIMIT 1 \gset

\echo '=== 1. ListContacts, first page (limit 20 + 1) ==='
EXPLAIN (ANALYZE, BUFFERS) EXECUTE list_contacts(:'tenant_a', '9999-12-31', 'ffffffff-ffff-ffff-ffff-ffffffffffff', 21);

\echo '=== 2. ListContacts, page after row 50,000 (keyset) ==='
EXPLAIN (ANALYZE, BUFFERS) EXECUTE list_contacts(:'tenant_a', :'deep_ts', :'deep_id', 21);

\echo '=== 3. For comparison only: the same page with OFFSET (not used by the API) ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT * FROM contacts WHERE tenant_id = :'tenant_a' AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC OFFSET 50000 LIMIT 21;

\echo '=== 4. SearchContacts q=nguyen (common: ~1 in 16 contacts) ==='
EXPLAIN (ANALYZE, BUFFERS) EXECUTE search_contacts(:'tenant_a', 'nguyen', '9999-12-31', 'ffffffff-ffff-ffff-ffff-ffffffffffff', 21);

\echo '=== 5. SearchContacts q=kh12345@ (rare: one email) ==='
EXPLAIN (ANALYZE, BUFFERS) EXECUTE search_contacts(:'tenant_a', 'kh12345@', '9999-12-31', 'ffffffff-ffff-ffff-ffff-ffffffffffff', 21);

\echo '=== 6. SearchContacts q=zzzzqq (no match: worst case, scans the whole tenant) ==='
EXPLAIN (ANALYZE, BUFFERS) EXECUTE search_contacts(:'tenant_a', 'zzzzqq', '9999-12-31', 'ffffffff-ffff-ffff-ffff-ffffffffffff', 21);

ROLLBACK;
