# GitHub Integration Design

> Status: In Design — not yet implemented.
> Follows the same architecture patterns as the LaunchDarkly integration.

---

## Overview

GitHub is a code source integration. One integration per org covers **all repos** under a given owner (org or user). Auth is via GitHub OAuth app (not API key). Data is fetched via GitHub REST API — no GraphQL.

**What we sync:** Merged pull requests within a date range, including metadata and a diff URL reference. Actual diff content is deferred to a future enrichment workflow.

---

## Integration Config

`integrations.source = 'github'`

`integrations.config`:
```json
{
  "owner": "acme-org"
}
```

Unique constraint: `UNIQUE(org_id, source, config->>'owner')` — one GitHub integration per owner per org.

---

## Table: `pull_requests`

Mirrors `feature_flags`. One row per PR, upserted on every ETL run.

| Column | Type | Notes |
|--------|------|-------|
| id | bigserial | PK |
| key | uuid UNIQUE | SHA1(org_id \| github \| pr.id) |
| source_record_key | uuid | → source_records.key |
| integration_id | uuid | → integrations.key |
| org_id | uuid | Tenant scoping |
| state | text | `merged \| closed` — only merged PRs fetched at sync time |
| data | jsonb | Normalized PR data (see below) |
| created_at | timestamptz | |
| updated_at | timestamptz | |

Key indexes: `(org_id)`, `(integration_id)`.

### Deterministic Key

```go
// key = SHA1(namespace, org_id|github|pr_id)
// pr.id is GitHub's globally unique numeric PR ID — stable across repo renames
ComputePullRequestKey(orgID uuid.UUID, sourcePRKey string) uuid.UUID
```

### `source_records` source type

`source_type = 'pr_detail'` — raw GitHub PR API response, append-only.

---

## Normalized Data Model — `pull_requests.data`

```json
{
  "title": "feat: add checkout v2",
  "description": "PR description...",

  "source": {
    "key": "987654321",
    "provider": "github",
    "project": "123456",
    "created_at": "2024-01-15T10:00:00Z",
    "merged_at": "2024-01-20T10:00:00Z"
  },

  "actor": {
    "authors": [
      { "login": "ariel", "email": "ariel@acme.com" }
    ],
    "coauthors": [
      { "login": "jordan", "email": "jordan@acme.com" }
    ]
  },

  "maintainer": {
    "team": { "name": "payments-team" }
  },

  "details": {
    "url": "https://github.com/acme-org/backend/pull/42",
    "number": 42,
    "repo": { "name": "backend" },
    "labels": ["feature", "payments"],
    "diff": {
      "url": "https://github.com/acme-org/backend/pull/42.diff",
      "sha": "abc123",
      "branches": {
        "source": "feat/checkout-v2",
        "target": "main"
      },
      "stats": {
        "additions": 120,
        "deletions": 45,
        "changed_files": 8
      }
    }
  }
}
```

### Field Notes

| Path | Source | Notes |
|------|--------|-------|
| source.key | `pull_request.id` | GitHub's globally unique numeric PR ID. Stable across repo renames |
| source.project | `repository.id` | Stable numeric repo ID. Stable across repo renames/transfers |
| source.merged_at | `pull_request.merged_at` | Source timestamp, never ingestion time |
| actor.authors | `pull_request.user` | PR opener. Single entry array for consistency |
| actor.coauthors | `pull_request.assignees` | Co-authors/collaborators from PR assignees |
| maintainer.team | `pull_request.requested_reviewers` team | Nullable |
| details.number | `pull_request.number` | Human-readable #42 — display only, not used in key |
| details.repo.name | `repository.name` | Display name — can change, stable ID is in source.project |
| details.diff.sha | `pull_request.merge_commit_sha` | Merge commit SHA — key for future LD cross-source correlation |
| details.diff.stats | `pull_request.additions/deletions/changed_files` | Available on single PR GET, no extra API call |

---

## Extract: Two-Phase Fetch (mirrors LD pattern)

### Why two phases

GitHub has no single endpoint that returns all PRs across all repos with full details. The available options are:

| Approach | Problem |
|----------|---------|
| `GET /orgs/{org}/repos` → per-repo `GET /pulls` | N+1 calls — one list call per repo before any PR data |
| `GET /search/issues` | Single call across all repos with date range filter, but returns partial data only |
| `GET /repos/{owner}/{repo}/pulls/{number}` | Full PR detail, but requires knowing the PR first |

**Decision:** Use the Search API to discover PRs, then parallel detail fetches — identical to how LD uses audit log pagination to discover flags then fetches flag detail in parallel.

### Phase 1 — SearchPullRequests (mirrors `ListAuditLogs`)

```
GET /search/issues?q=is:pr+is:merged+org:{owner}+merged:{from}..{to}&per_page=100
```

- Single call covers all repos in the org
- Date range filter built into the query
- Paginated via `Link` header (cursor-based, unlike LD's timestamp cursor)
- Returns enough to identify each PR: `id`, `number`, `title`, `html_url`, `diff_url`, `merged_at`, `repository_url`

**Tradeoff — 1000 result cap:** GitHub's Search API returns a maximum of 1000 results per query. For orgs with high PR volume, the extract workflow automatically narrows the date window until each window returns fewer than 1000 results. This adds complexity but is the only way to guarantee completeness without per-repo enumeration.

### Phase 2 — GetPullRequestDetail (mirrors `GetFlagDetail`)

```
GET /repos/{owner}/{repo}/pulls/{number}
```

- One call per PR, run in parallel via `workflow.Go` goroutines
- Returns full PR data: `merge_commit_sha`, `additions`, `deletions`, `changed_files`, `head`/`base` refs, `requested_reviewers`
- Raw response saved to `source_records` as `pr_detail` before normalization

---

## Workflow Architecture

```
FeatureFlagETLWorkflow (routes by source)
  └─→ GitHubExtractWorkflow
        ├─ SearchPullRequests — paginate search API for merged PRs in date range
        ├─ SaveSourceRecord (search result, pr_search) — raw search hit saved
        ├─ GetPullRequestDetail — parallel fetch per PR for full details
        ├─ SaveSourceRecord (full PR, pr_detail) — raw detail saved
        └─ For each PR → GitHubPRTransformWorkflow (fire-and-forget)
              ├─ NormalizePullRequest (GitHub-specific)
              └─ SavePullRequest (shared)
```

Same incremental sync pattern as LD: `last_synced_at` watermark on `integrations`. First run = full backfill. Subsequent runs = only PRs merged since last sync.

---

## Auth: GitHub OAuth App

> Detail TBD — to be designed next.

Key difference from LD: OAuth token instead of static API key. Token stored encrypted (AES-256-GCM) in `integrations.api_key`, same column, same encryption. OAuth flow handled at integration creation time in the Search Service.

---

## File Structure

Following LD naming conventions exactly:

```
models/github.go                        # GHPullRequest, GHRepo raw API types
activities/github/
  ├── activities.go                     # Activity container
  ├── client.go                         # HTTP client for GitHub REST API
  ├── search_pull_requests.go           # Paginate search API across org
  ├── get_pull_request_detail.go        # Fetch full PR detail
  ├── normalize_pull_request.go         # Map GHPullRequest → canonical PullRequest
  └── errors.go                         # Map GitHub errors → retryable/non-retryable
workflows/github/
  └── extract.go                        # GitHubExtractWorkflow
repository/
  └── postgres_pull_requests.go         # SavePullRequest
```

One routing case added to `workflows/etl.go`. Zero changes to LD code.

---

## Open Questions

| Question | Notes |
|----------|-------|
| OAuth app detail | OAuth app chosen. Flow, token refresh, and storage detail TBD |
| Search 1000 cap window strategy | Auto-narrow date window logic needs exact algorithm defined |
| Enrichment workflow | Future `GitHubEnrichWorkflow` — fetches diff content, cross-references with LD flag changes via `diff.sha` |
| `pull_requests` migration | New migration file alongside existing ones |
