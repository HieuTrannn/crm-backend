-- +goose Up

-- Target of the composite FK from deals: a deal's contact must be in the same tenant.
ALTER TABLE contacts ADD CONSTRAINT contacts_tenant_id_id_key UNIQUE (tenant_id, id);

CREATE TABLE deals (
    id                  uuid        PRIMARY KEY,
    tenant_id           uuid        NOT NULL REFERENCES tenants (id),
    contact_id          uuid        NOT NULL,
    title               text        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    amount              bigint      NOT NULL CHECK (amount >= 0), -- VND, integer only
    stage               text        NOT NULL DEFAULT 'lead'
                                    CHECK (stage IN ('lead', 'qualified', 'proposal', 'won', 'lost')),
    expected_close_date date,
    owner_id            uuid        NOT NULL,
    lost_reason         text        CHECK (char_length(lost_reason) <= 1000),
    version             integer     NOT NULL DEFAULT 1,
    closed_at           timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz, -- soft delete: kept for history, hidden everywhere else
    -- Invariants the state machine relies on, enforced even if code has a bug.
    CONSTRAINT deals_lost_reason_stage_check CHECK ((stage = 'lost') = (lost_reason IS NOT NULL)),
    CONSTRAINT deals_closed_at_stage_check CHECK ((stage IN ('won', 'lost')) = (closed_at IS NOT NULL)),
    CONSTRAINT deals_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT deals_contact_fkey FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id),
    CONSTRAINT deals_owner_fkey FOREIGN KEY (tenant_id, owner_id) REFERENCES users (tenant_id, id)
);

-- Dashboard and Kanban (PRD 5.4).
CREATE INDEX deals_tenant_stage_idx ON deals (tenant_id, stage);
-- Cursor pagination, newest first (FR-DEAL-05).
CREATE INDEX deals_tenant_created_idx ON deals (tenant_id, created_at DESC, id DESC);
-- Open-deal check when deleting a contact, and the contact_id filter.
CREATE INDEX deals_tenant_contact_idx ON deals (tenant_id, contact_id);

-- Append-only audit of stage changes (FR-DEAL-03). from_stage is NULL for creation.
CREATE TABLE deal_stage_history (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL,
    deal_id    uuid        NOT NULL,
    from_stage text        CHECK (from_stage IN ('lead', 'qualified', 'proposal', 'won', 'lost')),
    to_stage   text        NOT NULL CHECK (to_stage IN ('lead', 'qualified', 'proposal', 'won', 'lost')),
    reason     text        CHECK (char_length(reason) <= 1000),
    changed_by uuid        NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, deal_id) REFERENCES deals (tenant_id, id),
    FOREIGN KEY (tenant_id, changed_by) REFERENCES users (tenant_id, id)
);
CREATE INDEX deal_stage_history_deal_idx ON deal_stage_history (tenant_id, deal_id, changed_at);

ALTER TABLE deals ENABLE ROW LEVEL SECURITY;
ALTER TABLE deals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON deals
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE deal_stage_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE deal_stage_history FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON deal_stage_history
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON deals TO app_user;
-- History is append-only: no UPDATE or DELETE.
GRANT SELECT, INSERT ON deal_stage_history TO app_user;

-- +goose Down
DROP TABLE deal_stage_history;
DROP TABLE deals;
ALTER TABLE contacts DROP CONSTRAINT contacts_tenant_id_id_key;
