-- 003_analytics_indexes.up.sql
-- Adds composite index for analytics queries that filter changelog by org, state, and date range.
-- All four analytics queries (launched_wins, top_teams, velocity, audit_log) use this pattern.

CREATE INDEX idx_changelog_org_state_occurred_at
    ON feature_flag_changelog(org_id, state, occurred_at);
