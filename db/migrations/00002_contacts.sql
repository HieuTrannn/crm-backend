-- +goose Up

CREATE TABLE contacts (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id),
    full_name  text        NOT NULL CHECK (char_length(full_name) BETWEEN 1 AND 200),
    email      citext      CHECK (char_length(email) <= 254),
    phone      text        CHECK (char_length(phone) <= 20),
    company    text        CHECK (char_length(company) <= 200),
    notes      text        CHECK (char_length(notes) <= 5000),
    owner_id   uuid        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    -- FR-CON-03: lower-cased, unaccented "name email phone", computed on write so a
    -- search does not call unaccent per row. See docs/performance.md.
    search_text text GENERATED ALWAYS AS (
        immutable_unaccent(lower(full_name || ' ' || coalesce(email::text, '') || ' ' || coalesce(phone, '')))
    ) STORED,
    -- The owner must be a user of the same tenant (FK checks bypass RLS, so a plain
    -- FK on owner_id would accept another tenant's user).
    CONSTRAINT contacts_owner_fkey FOREIGN KEY (tenant_id, owner_id) REFERENCES users (tenant_id, id)
);

-- FR-CON-01: email unique per tenant among live contacts; other tenants may reuse it.
CREATE UNIQUE INDEX contacts_tenant_email_key ON contacts (tenant_id, email)
    WHERE deleted_at IS NULL;

-- FR-CON-02: cursor pagination, newest first, matches ORDER BY created_at DESC, id DESC.
CREATE INDEX contacts_tenant_created_idx ON contacts (tenant_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- No GIN trigram index on search_text: under RLS, LIKE is not LEAKPROOF, so PostgreSQL
-- never uses such an index for app_user; it would only slow down writes.

ALTER TABLE contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contacts
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- Soft delete only (FR-CON-04): no DELETE privilege.
GRANT SELECT, INSERT, UPDATE ON contacts TO app_user;

-- +goose Down
DROP TABLE contacts;
