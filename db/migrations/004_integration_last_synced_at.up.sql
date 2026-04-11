-- 004_integration_last_synced_at.up.sql
-- Adds last_synced_at to track the high-water mark for incremental syncs.
-- NULL means the integration has never completed a sync (triggers full backfill).

ALTER TABLE integrations
    ADD COLUMN last_synced_at TIMESTAMPTZ NULL;
