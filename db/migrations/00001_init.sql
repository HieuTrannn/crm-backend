-- +goose Up

-- Trusted extensions (PG13+): the database owner can create them without superuser.
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent() is only STABLE (it depends on search_path and the dictionary), so it cannot
-- be used in an index. Pinning the schema-qualified dictionary makes this wrapper safe to
-- mark IMMUTABLE. The SQL-standard body is resolved once at creation time.
CREATE FUNCTION immutable_unaccent(text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    RETURN public.unaccent('public.unaccent'::regdictionary, $1);

CREATE TABLE tenants (
    id         uuid        PRIMARY KEY,
    name       text        NOT NULL,
    slug       text        NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            uuid        PRIMARY KEY,
    tenant_id     uuid        NOT NULL REFERENCES tenants (id),
    email         citext      NOT NULL UNIQUE,
    password_hash text        NOT NULL,
    full_name     text        NOT NULL,
    role          text        NOT NULL CHECK (role IN ('owner', 'member')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- Target of the composite FK below; also serves "users of a tenant" lookups.
    UNIQUE (tenant_id, id)
);

CREATE TABLE refresh_tokens (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL,
    tenant_id  uuid        NOT NULL,
    token_hash text        NOT NULL UNIQUE,
    family_id  uuid        NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    -- FK checks bypass RLS, so a plain FK on user_id would accept another tenant's user.
    -- The composite FK forces the token's tenant to match its user's tenant.
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX refresh_tokens_tenant_family_idx ON refresh_tokens (tenant_id, family_id);

-- Row-Level Security (NFR-ISO-01..03). Unset tenant => NULL => no rows (fail closed).
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON users
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE refresh_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE refresh_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON refresh_tokens
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- Login looks a user up by email before the tenant is known. This function runs as its
-- owner (the migration role). Because of FORCE, the owner is also subject to RLS, so it
-- gets one extra SELECT policy that is active only while the function turns
-- app.auth_lookup on. app_user cannot use this policy: it is granted TO the owner.
-- +goose StatementBegin
DO $do$
BEGIN
    EXECUTE format(
        'CREATE POLICY auth_lookup ON users FOR SELECT TO %I '
        'USING (current_setting(''app.auth_lookup'', true) = ''on'')',
        current_user);
END
$do$;
-- +goose StatementEnd

-- Risks of SECURITY DEFINER and how they are limited:
--  * search_path hijack: pinned to pg_catalog, public, pg_temp (pg_temp last).
--  * over-exposure: returns only the columns login needs, never full_name/created_at.
--  * open to everyone by default: EXECUTE is revoked from PUBLIC, granted to app_user only.
-- +goose StatementBegin
CREATE FUNCTION auth_find_user_by_email(p_email citext)
    RETURNS TABLE (id uuid, tenant_id uuid, password_hash text, role text)
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, public, pg_temp
AS $$
BEGIN
    -- A "SET app.auth_lookup = 'on'" function clause needs superuser for custom
    -- parameters (fails on Neon), so the flag is toggled around the query instead.
    PERFORM set_config('app.auth_lookup', 'on', true);
    RETURN QUERY
        SELECT u.id, u.tenant_id, u.password_hash, u.role
        FROM public.users u
        WHERE u.email = p_email;
    PERFORM set_config('app.auth_lookup', '', true);
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION auth_find_user_by_email(citext) FROM PUBLIC;

-- app_user: the role the API connects as. Not the table owner, no BYPASSRLS (NFR-ISO-03).
-- Roles are cluster-wide, so creation is skipped when it already exists (e.g. on Neon,
-- where it can be created in the Console first). The password comes from
-- APP_USER_PASSWORD via goose ENVSUB; it must not contain ' \ or $.
-- ENVSUB stays on for this one line only: it would rewrite the $$ of dollar-quoted bodies.
-- +goose ENVSUB ON
SELECT set_config('crm.app_user_password', '${APP_USER_PASSWORD}', true);
-- +goose ENVSUB OFF
-- +goose StatementBegin
DO $do$
DECLARE
    pw text := current_setting('crm.app_user_password');
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        IF pw = '' THEN
            RAISE EXCEPTION 'APP_USER_PASSWORD must be set to create role app_user';
        END IF;
        EXECUTE format('CREATE ROLE app_user LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD %L', pw);
    END IF;
END
$do$;
-- +goose StatementEnd
SELECT set_config('crm.app_user_password', '', true);

-- Minimal grants: no DELETE anywhere, no TRUNCATE, no DDL.
GRANT USAGE ON SCHEMA public TO app_user;
GRANT SELECT, INSERT ON tenants TO app_user;
GRANT SELECT, INSERT, UPDATE ON users TO app_user;
GRANT SELECT, INSERT, UPDATE ON refresh_tokens TO app_user;
GRANT EXECUTE ON FUNCTION auth_find_user_by_email(citext) TO app_user;
GRANT EXECUTE ON FUNCTION immutable_unaccent(text) TO app_user;

-- +goose Down
REVOKE ALL ON SCHEMA public FROM app_user;
DROP FUNCTION auth_find_user_by_email(citext);
DROP TABLE refresh_tokens;
DROP TABLE users;
DROP TABLE tenants;
DROP FUNCTION immutable_unaccent(text);
-- Fails if app_user still holds privileges in another database of the same cluster.
DROP ROLE IF EXISTS app_user;
DROP EXTENSION IF EXISTS unaccent;
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS citext;
