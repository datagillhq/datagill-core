-- 007_pr_enrichment.up.sql
-- Creates pull_request_enrichments table for flag-finding enrichment results.

CREATE TABLE pull_request_enrichments (
    pull_request_key UUID        PRIMARY KEY REFERENCES pull_requests(key) ON DELETE CASCADE,
    org_id           UUID        NOT NULL REFERENCES organizations(key) ON DELETE CASCADE,
    status           TEXT        NOT NULL DEFAULT 'pending',
    attempts         INT         NOT NULL DEFAULT 0,
    enrichment       JSONB,
    enriched_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_pr_enrichments_status ON pull_request_enrichments(status)
  WHERE status = 'pending';

CREATE INDEX idx_pr_enrichments_org_id ON pull_request_enrichments(org_id);

CREATE INDEX idx_pr_enrichment_flags_added ON pull_request_enrichments
  USING GIN ((enrichment->'feature_flags'->'added'));

CREATE INDEX idx_pr_enrichment_flags_removed ON pull_request_enrichments
  USING GIN ((enrichment->'feature_flags'->'removed'));
