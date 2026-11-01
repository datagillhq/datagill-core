-- 009_pr_enrichment_next_attempt_at.up.sql
-- Adds next_attempt_at to pull_request_enrichments for retry backoff.
--
-- A failed enrichment waits longer before each retry (30m doubling, capped at 24h), so a
-- multi-day GitHub outage does not use up every PR's attempts within a few hours.
-- Pending rows are only claimable once next_attempt_at <= NOW().
--
-- Defaults to NOW() so existing and newly ingested rows are claimable immediately.
-- No new index: listing is already scoped by idx_pr_enrichments_org_id, and the table is
-- small; add one when a slow query proves the need.

ALTER TABLE pull_request_enrichments
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
