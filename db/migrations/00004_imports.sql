-- +goose Up

CREATE TABLE import_jobs (
    id             uuid        PRIMARY KEY,
    tenant_id      uuid        NOT NULL REFERENCES tenants (id),
    created_by     uuid        NOT NULL,
    status         text        NOT NULL DEFAULT 'pending'
                               CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    total_rows     integer     NOT NULL DEFAULT 0,
    processed_rows integer     NOT NULL DEFAULT 0,
    success_rows   integer     NOT NULL DEFAULT 0,
    error_rows     integer     NOT NULL DEFAULT 0,
    -- Why a job ended as failed or cancelled; NULL otherwise.
    failure_reason text        CHECK (char_length(failure_reason) <= 1000),
    created_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz,
    CONSTRAINT import_jobs_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT import_jobs_created_by_fkey FOREIGN KEY (tenant_id, created_by) REFERENCES users (tenant_id, id)
);

-- One row per rejected CSV line (FR-IMP-03). row_number is the line in the file;
-- the primary key also serves the "errors of a job, by line" listing.
CREATE TABLE import_job_errors (
    job_id     uuid    NOT NULL,
    tenant_id  uuid    NOT NULL,
    row_number integer NOT NULL,
    reason     text    NOT NULL CHECK (char_length(reason) <= 1000),
    PRIMARY KEY (job_id, row_number),
    FOREIGN KEY (tenant_id, job_id) REFERENCES import_jobs (tenant_id, id)
);

ALTER TABLE import_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON import_jobs
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE import_job_errors ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_job_errors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON import_job_errors
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON import_jobs TO app_user;
GRANT SELECT, INSERT ON import_job_errors TO app_user;

-- +goose Down
DROP TABLE import_job_errors;
DROP TABLE import_jobs;
