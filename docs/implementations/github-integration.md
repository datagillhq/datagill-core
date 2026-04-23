# GitHub Integration Design

> Status: In Design — not yet implemented.
> Follows the same architecture patterns as the LaunchDarkly integration.

---

## Overview

GitHub is a code source integration. One integration per DataGill org covers one GitHub owner (organization or user account) and all repositories visible under that owner. A single DataGill org may connect multiple GitHub owners via multiple integrations. Auth is via GitHub OAuth app (not API key). Data is fetched via GitHub REST API — no GraphQL.

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

Unique constraint: `UNIQUE(org_id, source, config->>'owner')` — one GitHub integration per owner per DataGill org.

---

## Table: `pull_requests`

Mirrors `feature_flags`. One row per PR, upserted on every ETL run.

| Column | Type | Notes |
|--------|------|-------|
| id | bigserial | PK |
| key | uuid UNIQUE | SHA1(org_id \| github \| repository_id \| pull_number) |
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
// key = SHA1(namespace, org_id|github|repository_id|pull_number)
// pull numbers are only unique within a repository, so repository identity
// must be part of the deterministic key. repository_id is stable across renames.
func ComputePullRequestKey(orgID uuid.UUID, repositoryID int64, pullNumber int) uuid.UUID
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
    "key": "123456:42",
    "provider": "github",
    "repository_id": "123456",
    "created_at": "2024-01-15T10:00:00Z",
    "merged_at": "2024-01-20T10:00:00Z"
  },

  "actor": {
    "author": {
      "login": "ariel",
      "id": 12345
    }
  },

  "details": {
    "url": "https://github.com/acme-org/backend/pull/42",
    "number": 42,
    "repository": {
      "owner": "acme-org",
      "name": "backend"
    },
    "labels": ["feature", "payments"],
    "assignees": [
      { "login": "jordan", "id": 67890 }
    ],
    "requested_reviewers": {
      "users": [
        { "login": "sarah", "id": 11111 }
      ],
      "teams": [
        { "slug": "payments-team", "name": "Payments Team" }
      ]
    },
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
| source.key | `repository.id + ":" + pull_request.number` | Stable per-repository PR identifier used for deterministic keys |
| source.repository_id | `repository.id` | Stable numeric repo ID. Stable across repo renames/transfers |
| source.merged_at | `pull_request.merged_at` | Source timestamp, never ingestion time |
| actor.author | `pull_request.user` | PR opener |
| details.number | `pull_request.number` | Human-readable #42 — display only, scoped by repository |
| details.repository.name | `repository.name` | Display name — can change, stable ID is in `source.repository_id` |
| details.assignees | `pull_request.assignees` | Assignment metadata only — not authorship |
| details.requested_reviewers.users | `pull_request.requested_reviewers` | Pending requested user reviewers |
| details.requested_reviewers.teams | `pull_request.requested_teams` | Pending requested team reviewers |
| details.diff.sha | `pull_request.merge_commit_sha` | Merge commit SHA — key for future LD cross-source correlation |
| details.diff.stats | `pull_request.additions/deletions/changed_files` | Available on single PR GET, no extra API call |

Deferred fields: co-authors and completed reviewers. Do not infer co-authorship from assignees.

---

## Extract: Two-Phase Fetch (mirrors LD pattern)

### Why two phases

GitHub has no single endpoint that returns full PR detail across all repos for an owner. The available options are:

| Approach | Problem |
|----------|---------|
| `GET /orgs/{org}/repos` → per-repo `GET /pulls` | N+1 calls — one list call per repo before any PR data |
| `GET /search/issues` | Owner-wide discovery with qualifiers like `is:pull-request`, but returns search-index data only and has hard limits |
| `GET /repos/{owner}/{repo}/pulls/{number}` | Full PR detail, but requires knowing the PR first |

**Decision:** Use the Search API as the primary discovery path, then parallel detail fetches. This mirrors LD's discover-then-hydrate pattern, but GitHub search has hard limits, so the design must explicitly account for incomplete search coverage.

### Phase 1 — SearchPullRequests (mirrors `ListAuditLogs`)

```
GET /search/issues?q=is:pull-request+is:merged+org:{owner}+merged:{from}..{to}&per_page=100
```

- A single query can discover PRs across repositories for the owner
- Date range filter built into the query
- Paginated with `page` / `per_page`
- Returns enough to identify each PR for phase-two hydration, but it is still a search endpoint, not a PR-detail endpoint

**Tradeoffs and limits:**

- Search returns at most 1000 results per query, so the workflow must narrow the date window when a query approaches that cap.
- Search only searches up to 4000 matching repositories.
- Search may return `incomplete_results=true` if the query times out.

Because of those limits, date-window narrowing alone does not guarantee completeness for very large owners. The implementation should use search-first discovery by default, but explicitly fall back to repository enumeration when search bounds are exceeded or results are incomplete.

### Phase 2 — GetPullRequestDetail (mirrors `GetFlagDetail`)

```
GET /repos/{owner}/{repo}/pulls/{number}
```

- One call per PR, run in parallel via `workflow.Go` goroutines
- Returns full PR data: `merge_commit_sha`, `additions`, `deletions`, `changed_files`, `head`/`base` refs, assignees, requested reviewers, requested teams
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

Same incremental sync pattern as LD: a merged-at cursor stored in Temporal workflow state, not a DB column on `integrations`. First run = bounded backfill. Subsequent runs = only PRs merged since the last successful sync window.

---

## Auth: GitHub OAuth App

> Detail TBD — to be designed next.

Key difference from LD: OAuth token instead of static API key. Token stored encrypted (AES-256-GCM) in `integrations.api_key`, same column, same encryption. OAuth flow handled at integration creation time in the Search Service.

Recommended token lifecycle:

- Persist encrypted OAuth credential material on the integration row.
- Refresh access tokens inside activities immediately before API calls when needed.
- Persist rotated tokens through the repository layer.
- Do not cache long-lived access tokens in Temporal workflow state.

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
| Search fallback threshold | Exact fallback rule from search-first to per-repo enumeration needs definition |
| Enrichment workflow | Future `GitHubEnrichWorkflow` — fetches diff content, cross-references with LD flag changes via `diff.sha` |
| `pull_requests` migration | New migration file alongside existing ones |
