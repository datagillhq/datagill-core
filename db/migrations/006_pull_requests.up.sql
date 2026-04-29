-- 006_pull_requests.up.sql
-- Creates pull request storage for GitHub ETL.

CREATE TABLE pull_requests (
    id                BIGSERIAL PRIMARY KEY,
    key               UUID NOT NULL UNIQUE,
    source_record_key UUID NOT NULL,
    integration_id    UUID NOT NULL REFERENCES integrations(key) ON DELETE CASCADE,
    org_id            UUID NOT NULL REFERENCES organizations(key) ON DELETE CASCADE,
    state             TEXT NOT NULL,
    data              JSONB NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_pull_requests_org_id ON pull_requests(org_id);
CREATE INDEX idx_pull_requests_integration_id ON pull_requests(integration_id);
