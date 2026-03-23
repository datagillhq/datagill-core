-- 002_indexes.up.sql
-- Creates indexes for foreign key lookups and common query patterns.

-- Integrations: filter by org
CREATE INDEX idx_integrations_org_id ON integrations(org_id);

-- Source records: filter by org, integration, and time-based queries
CREATE INDEX idx_source_records_org_id ON source_records(org_id);
CREATE INDEX idx_source_records_integration_id ON source_records(integration_id);
CREATE INDEX idx_source_records_created_at ON source_records(created_at);

-- Feature flags: filter by org and integration
CREATE INDEX idx_feature_flags_org_id ON feature_flags(org_id);
CREATE INDEX idx_feature_flags_integration_id ON feature_flags(integration_id);

-- Changelog: time-series queries scoped to org or specific flag
CREATE INDEX idx_changelog_org_id_occurred_at ON feature_flag_changelog(org_id, occurred_at);
CREATE INDEX idx_changelog_feature_flag_key_occurred_at ON feature_flag_changelog(feature_flag_key, occurred_at);
