-- 007_pr_enrichment.up.sql
-- Adds enrichment columns to pull_requests for flag-finding results.

ALTER TABLE pull_requests
  ADD COLUMN enrichment  JSONB,
  ADD COLUMN enriched_at TIMESTAMPTZ,
  ADD COLUMN status      TEXT NOT NULL DEFAULT 'pending',
  ADD COLUMN attempts    INT  NOT NULL DEFAULT 0;

CREATE INDEX idx_pull_requests_status ON pull_requests(status)
  WHERE status = 'pending';

CREATE INDEX idx_pr_enrichment_flags_added ON pull_requests
  USING GIN ((enrichment->'feature_flags'->'added'));

CREATE INDEX idx_pr_enrichment_flags_removed ON pull_requests
  USING GIN ((enrichment->'feature_flags'->'removed'));
