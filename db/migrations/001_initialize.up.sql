-- 001_initialize.up.sql
-- Creates the core DataGill tables for feature flag management.
--
-- MIGRATION POLICY: Fix forward, never rollback.
-- If a migration fails, create a new migration to fix the issue.
-- DOWN migrations are not provided — they risk data loss and are
-- rarely tested. For local dev reset: DROP DATABASE and re-run all UP.

-- Organizations table: top-level tenant container
CREATE TABLE organizations (
    id              BIGSERIAL PRIMARY KEY,
    key             UUID NOT NULL UNIQUE,
    external_org_id TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Integrations table: provider connections per org
CREATE TABLE integrations (
    id         BIGSERIAL PRIMARY KEY,
    key        UUID NOT NULL UNIQUE,
    org_id     UUID NOT NULL REFERENCES organizations(key) ON DELETE CASCADE,
    source     TEXT NOT NULL,
    api_key    TEXT NOT NULL,
    config     JSONB NOT NULL DEFAULT '{}',
    state      TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Source records table: append-only raw API responses
CREATE TABLE source_records (
    id             BIGSERIAL PRIMARY KEY,
    key            UUID NOT NULL UNIQUE,
    integration_id UUID NOT NULL REFERENCES integrations(key) ON DELETE CASCADE,
    org_id         UUID NOT NULL REFERENCES organizations(key) ON DELETE CASCADE,
    source_type    TEXT NOT NULL,
    data           JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
    -- No updated_at: this table is append-only
);

-- Feature flags table: normalized flag state
CREATE TABLE feature_flags (
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

-- Feature flag changelog table: normalized change events
CREATE TABLE feature_flag_changelog (
    id                BIGSERIAL PRIMARY KEY,
    key               UUID NOT NULL UNIQUE,
    source_record_key UUID NOT NULL,
    feature_flag_key  UUID NOT NULL,
    integration_id    UUID NOT NULL REFERENCES integrations(key) ON DELETE CASCADE,
    org_id            UUID NOT NULL REFERENCES organizations(key) ON DELETE CASCADE,
    event_type        TEXT NOT NULL,
    state             TEXT NOT NULL,
    occurred_at       TIMESTAMPTZ NOT NULL,
    data              JSONB NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
