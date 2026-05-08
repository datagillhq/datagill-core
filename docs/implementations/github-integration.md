# GitHub Integration Design

> Status: In Design — not yet implemented.
> Follows the same architecture patterns as the LaunchDarkly integration.
> Implementation sequencing lives in [github-integration-implementation-plan.md](./github-integration-implementation-plan.md).
> The first front-facing API for ingested PR data is planned in [github-pr-analytics-api.md](./github-pr-analytics-api.md).

---

## Overview

GitHub is a code source integration. DataGill operates a single GitHub App registration, and each customer connects GitHub by installing that app on a GitHub organization or personal account. One integration row represents one GitHub App installation. A single DataGill org may connect multiple GitHub installations. Data is fetched via GitHub REST API — no GraphQL.

**What we sync:** Merged pull requests within a date range, including metadata and a diff URL reference. Actual diff content is deferred to a future enrichment workflow.

**GitHub App permissions for v1:** repository `Metadata: read` and repository `Pull requests: read`. Keep the app read-only. During the onboarding/search spike, verify that `GET /search/issues` works with that permission set under an installation token. If GitHub requires additional read-only permission for search in practice, document and add the minimum extra read permission then.

**Why GitHub App, not OAuth app:** GitHub's current guidance is to prefer GitHub Apps over OAuth apps for new integrations. GitHub Apps provide fine-grained permissions, installation-scoped repository access, short-lived installation tokens, and centralized webhooks. OAuth apps use broader user-scoped permissions, are less precise about repo access, and are not the recommended greenfield integration model.

### Validated Spike Findings — 2026-04-24

The onboarding spike validated the following against a real GitHub App installation in `datagillhq`:

- callback query params include `code`, `installation_id`, `setup_action`, and `state`
- `request_oauth_on_install=true` works as expected for the combined install + authorize flow
- Search can exchange `code` for a GitHub App user access token
- Search can verify the installing user's installation visibility via `GET /user/installations`
- Search can fetch installation metadata via app JWT auth and mint an installation access token
- `GET /search/issues` succeeds with the minimal read-only permission set:
  - repository `Metadata: read`
  - repository `Pull requests: read`

Result: the v1 onboarding and discovery design is proven end to end. No broader repository, organization, or account permissions are currently required.

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
// org_id remains part of the key so DataGill can safely correlate data
// inside tenant scope without ever relying on cross-tenant global identity.
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
| details.diff.sha | `pull_request.merge_commit_sha` | Merge commit SHA — key for future LD cross-source correlation |
| details.diff.stats | `pull_request.additions/deletions/changed_files` | Available on single PR GET, no extra API call |

Requested reviewers, completed reviewers, co-authors, and review summaries are intentionally deferred to P1 enrichment. The initial GitHub integration should focus on reliable installation auth, merged PR discovery, normalization, and storage first.

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
- Assumption for v1: when called with an installation access token, search results are limited to repositories visible to that installation. If this assumption is wrong in practice, repository enumeration over the installation's accessible repositories becomes mandatory.

Because of those limits, date-window narrowing alone does not guarantee completeness for very large installations. The implementation should use search-first discovery by default, but explicitly fall back to repository enumeration over the installation's accessible repositories when search bounds are exceeded or results are incomplete.

### Phase 2 — GetPullRequestDetail (mirrors `GetFlagDetail`)

```
GET /repos/{owner}/{repo}/pulls/{number}
```

- One call per PR, run in parallel via `workflow.Go` goroutines
- Returns full PR data: `merge_commit_sha`, `additions`, `deletions`, `changed_files`, `head`/`base` refs, assignees
- Raw response saved to `source_records` as `pr_detail` before normalization

P1 enrichment, not part of the initial implementation:

- Completed reviewers via `GET /repos/{owner}/{repo}/pulls/{number}/reviews`
- Co-authors via PR commits and/or commit trailers
- Review summary derived from completed review states

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

### Rate Limiting and Concurrency

Initial implementation should hardcode conservative limits and make them configurable later:

- Search requests: sequential within a single date window
- PR detail fetches: max 10 concurrent requests per workflow run
- On GitHub rate limiting, rely on Temporal retries with backoff rather than custom retry loops
- If a run cannot complete because rate limits persist, fail the run and resume from the last successful merged-at cursor on retry

---

## Lifecycle and Drift

### Uninstall

GitHub integration removal is initiated from DataGill's manage integrations UI, consistent with existing LaunchDarkly integration management.

- User disables or removes the GitHub integration in DataGill
- DataGill marks the integration inactive and stops the Temporal schedule
- If DataGill later supports uninstalling the GitHub App directly from the product, that action should also remove or deactivate the matching integration row

### Permission Drift

Permission drift is deferred to P1.

- Repository selection changes after installation
- Installation permissions reduced
- Installation suspended or removed outside DataGill

For the initial implementation, ETL failures caused by these cases should surface clearly in logs and workflow failures, and the user can manually reconnect or refresh the integration.

---

## Auth: DataGill GitHub App

DataGill uses a single GitHub App registration. Customers connect GitHub by installing that app on an organization or personal account. Each installation becomes one `github` integration row in DataGill.

### Onboarding Security Decision

V1 should use the GitHub App callback URL with `request_oauth_on_install=true`, not the setup URL.

- GitHub's setup URL only guarantees an `installation_id`, and GitHub explicitly warns that this value can be spoofed.
- GitHub's callback URL is used for the web application flow that returns an authorization `code` and `state`.
- DataGill should use that one-time user authorization only to verify that the installing user can actually access the installation they just created.
- ETL remains installation-token based. DataGill should not persist long-lived user tokens for sync.

### Connection Flow

1. Frontend calls `POST /v1/auth/github` on the Search service.
2. Search verifies the authenticated org context, mints a short-lived signed `state`, and returns a GitHub installation URL that includes that `state`.
3. Frontend redirects the browser to GitHub's GitHub App installation flow.
4. User installs the app and authorizes it as part of the same GitHub flow.
5. GitHub redirects back to `GET /v1/auth/github` on the Search service with an authorization `code` and `state`.
6. Search exchanges the `code` for a short-lived GitHub App user access token.
7. Search verifies that the installation belongs to an installation visible to that authorized user.
8. Search fetches authoritative installation details, creates or updates the `github` integration row, creates/resumes the Temporal schedule, and redirects back to the frontend.

### Stored In `integrations.config`

- `installation_id`
- `account_login`
- `account_type`
- `repository_selection`

### Stored Outside the Database

- GitHub App ID
- GitHub App private key
- GitHub App client ID / client secret
- short-lived onboarding `state`
- one-time GitHub App user access tokens used during onboarding verification
- Installation access tokens

### Token Lifecycle

- Activities authenticate as the GitHub App using the app ID and private key.
- Activities generate an installation access token for the integration's `installation_id`.
- Installation access tokens are short-lived and used only for API calls during ETL.
- Search uses a one-time GitHub App user access token during onboarding to verify the installation belongs to the installing user, then discards it.
- Do not store installation access tokens in Postgres.
- Do not store onboarding user access tokens in Postgres.
- Do not pass installation access tokens through Temporal workflow state or history.

### User Authorization

Persistent user-scoped operation is out of scope for v1. However, one-time user authorization during installation is in scope because GitHub recommends it for verifying that the installation associated with the callback actually belongs to the installing user. DataGill should discard the resulting user access token after onboarding verification completes.

### Implementation Note

The GitHub App installation flow is a standard protocol surface, not a novel auth problem. The remaining work is to fit GitHub's installation callback and installation-token lifecycle cleanly into DataGill's existing Search service and Temporal worker split.

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
| GitHub App callback contract | Final callback query-param contract between GitHub and Search during install/update flow |
| Search fallback threshold | Exact fallback rule from search-first to per-repo enumeration needs definition |
| P1 enrichment workflow | Future `GitHubEnrichWorkflow` — completed reviewers, co-authors, diff content, and LD cross-reference via `diff.sha` |
| `pull_requests` migration | New migration file alongside existing ones |
