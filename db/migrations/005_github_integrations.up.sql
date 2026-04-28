-- 005_github_integrations.up.sql
-- Enforces one active/paused GitHub App installation per org.

CREATE UNIQUE INDEX idx_integrations_github_installation
ON integrations (org_id, source, ((config->>'installation_id')))
WHERE source = 'github' AND state != 'deleted';
