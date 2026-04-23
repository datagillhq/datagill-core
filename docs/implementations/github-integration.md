# GitHub Integration Design

> Status: In Design — not yet implemented.
> Follows the same architecture patterns as the LaunchDarkly integration.

---

## Overview

GitHub is a code source integration. DataGill operates a single GitHub App registration, and each customer connects GitHub by installing that app on a GitHub organization or personal account. One integration row represents one GitHub App installation. A single DataGill org may connect multiple GitHub installations. Data is fetched via GitHub REST API — no GraphQL.

**What we sync:** Merged pull requests within a date range, including metadata and a diff URL reference. Actual diff content is deferred to a future enrichment workflow.

**Why GitHub App, not OAuth app:** GitHub's current guidance is to prefer GitHub Apps over OAuth apps for new integrations. GitHub Apps provide fine-grained permissions, installation-scoped repository access, short-lived installation tokens, and centralized webhooks. OAuth apps use broader user-scoped permissions, are less precise about repo access, and are not the recommended greenfield integration model.

---

## Integration Config

`integrations.source = 'github'`

`integrations.config`:
```json
{
  "installation_id": 12345678,
  "account_login": "acme-org",
  "account_type": "Organization",
  "repository_selection": "selected"
}
```

Unique constraint: `UNIQUE(org_id, source, (config->>'installation_id'))` — one GitHub integration per installation per DataGill org.

---

## Table: `pull_requests`

Mirrors `feature_flags`. One row per PR, upserted on every ETL run.

| Column | Type | Notes |
|--------|------|-------|
| id | bigserial | PK |
| key | uuid UNIQUE | SHA1(org_id \| github \| pull_request.id) |
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
// key = SHA1(namespace, org_id|github|pull_request.id)
// pull_request.id is GitHub's globally unique numeric PR ID.
func ComputePullRequestKey(orgID uuid.UUID, sourcePRKey string) uuid.UUID
```

### `source_records` source type

`source_type = 'pr_detail'` — raw GitHub PR API response, append-only.

### Secret Boundary

The GitHub App private key is global DataGill secret material, not customer integration data.

- Store GitHub App credentials outside Postgres.
- Local/self-hosted: env vars or Docker secrets.
- Cloud: AWS Secrets Manager when available.

Customer-specific `integrations.config` stores installation metadata only.

---

## Normalized Data Model — `pull_requests.data`

```json
{
  "title": "feat: add checkout v2",
  "description": "PR description...",

  "source": {
    "key": "987654321",
    "provider": "github",
    "repository_id": "123456",
    "created_at": "2024-01-15T10:00:00Z",
    "merged_at": "2024-01-20T10:00:00Z"
  },

  "actor": {
    "author": {
      "login": "ariel",
      "id": 12345,
      "email": "ariel@acme.com"
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
    "completed_reviewers": {
      "users": [
        { "login": "sarah", "id": 11111 }
      ]
    },
    "coauthors": [
      { "login": "jordan", "id": 67890, "email": "jordan@acme.com" }
    ],
    "review_summary": {
      "approvals": 1,
      "changes_requested": 0
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
| source.key | `pull_request.id` | GitHub's globally unique numeric PR ID used for deterministic keys |
| source.repository_id | `repository.id` | Stable numeric repo ID. Stable across repo renames/transfers |
| source.merged_at | `pull_request.merged_at` | Source timestamp, never ingestion time |
| actor.author | `pull_request.user` | PR opener. Keep GitHub-native `id` and `login`; `email` is nullable |
| details.number | `pull_request.number` | Human-readable #42 — display only, scoped by repository |
| details.repository.name | `repository.name` | Display name — can change, stable ID is in `source.repository_id` |
| details.assignees | `pull_request.assignees` | Assignment metadata only — not authorship |
| details.completed_reviewers.users | `GET /repos/{owner}/{repo}/pulls/{number}/reviews` | Actual reviewers who submitted reviews on the PR |
| details.coauthors | Derived from PR commits and/or commit trailers | Do not infer from assignees |
| details.review_summary | Derived from completed review states | Rollup of approvals / change requests |
| details.diff.sha | `pull_request.merge_commit_sha` | Merge commit SHA — key for future LD cross-source correlation |
| details.diff.stats | `pull_request.additions/deletions/changed_files` | Available on single PR GET, no extra API call |

Requested reviewers are intentionally omitted from the base model for now. Completed reviewers are higher-signal because they reflect actual PR participation.

---

## Extract: Two-Phase Fetch (mirrors LD pattern)

### Why two phases

GitHub has no single endpoint that returns full PR detail across all repos for a GitHub App installation. The available options are:

| Approach | Problem |
|----------|---------|
| Installation repo enumeration → per-repo `GET /pulls` | N+1 calls — one list call per repo before any PR data |
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

Because of those limits, date-window narrowing alone does not guarantee completeness for very large installations. The implementation should use search-first discovery by default, but explicitly fall back to repository enumeration over the installation's accessible repositories when search bounds are exceeded or results are incomplete.

### Phase 2 — GetPullRequestDetail (mirrors `GetFlagDetail`)

```
GET /repos/{owner}/{repo}/pulls/{number}
```

- One call per PR, run in parallel via `workflow.Go` goroutines
- Returns full PR data: `merge_commit_sha`, `additions`, `deletions`, `changed_files`, `head`/`base` refs, assignees
- Raw response saved to `source_records` as `pr_detail` before normalization
- Completed reviewers require an additional call to `GET /repos/{owner}/{repo}/pulls/{number}/reviews`
- Co-authors require extra derivation from PR commits and/or commit trailers

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

## Auth: DataGill GitHub App

DataGill uses a single GitHub App registration. Customers connect GitHub by installing that app on an organization or personal account. Each installation becomes one `github` integration row in DataGill.

### Connection Flow

1. User clicks `Connect GitHub` in DataGill.
2. Frontend opens GitHub's browser-based GitHub App installation flow.
3. User selects the GitHub organization or personal account and chooses all repositories or selected repositories.
4. GitHub redirects back to DataGill's setup URL.
5. DataGill creates or updates the `github` integration row with installation metadata.

### Stored In `integrations.config`

- `installation_id`
- `account_login`
- `account_type`
- `repository_selection`

### Stored Outside the Database

- GitHub App ID
- GitHub App private key
- GitHub App client ID / client secret if later needed for optional user authorization
- Installation access tokens

### Token Lifecycle

- Activities authenticate as the GitHub App using the app ID and private key.
- Activities generate an installation access token for the integration's `installation_id`.
- Installation access tokens are short-lived and used only for API calls during ETL.
- Do not store installation access tokens in Postgres.
- Do not pass installation access tokens through Temporal workflow state or history.

### User Authorization

Out of scope for v1. The initial integration uses installation-scoped access only. If DataGill later needs to act on behalf of specific users or access user-scoped data, GitHub App user authorization can be added separately.

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
| GitHub App setup flow | Final callback/setup URL contract between frontend and backend |
| Search fallback threshold | Exact fallback rule from search-first to per-repo enumeration needs definition |
| Enrichment workflow | Future `GitHubEnrichWorkflow` — fetches diff content, cross-references with LD flag changes via `diff.sha` |
| `pull_requests` migration | New migration file alongside existing ones |
