-- 008_feature_flag_source_key_index.up.sql
-- Adds a composite expression index to support enrichment flag key lookups.
--
-- Supports:
--   GetFeatureFlagsBySourceKeys: WHERE org_id = $1 AND data->'source'->>'key' = ANY(...) AND state != 'deleted'
--   ListFeatureFlagSourceKeys:   WHERE org_id = $1 AND state != 'deleted'

CREATE INDEX idx_feature_flags_org_source_key
ON feature_flags(org_id, (data->'source'->>'key'))
WHERE state != 'deleted';
