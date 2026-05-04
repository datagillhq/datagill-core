# GitHub PR Enrichment — Implementation Plan

> Status: Ready for implementation.
> Companion to [github-pr-enrichment.md](./github-pr-enrichment.md).

This document breaks the enrichment design into concrete, ordered tasks. Each task is independently landable and keeps `main` releasable.

---

## What Already Exists

- `pull_requests` table (`006_pull_requests.up.sql`) — no enrichment table yet.
- `models/pull_request.go` — `PullRequest` struct, no enrichment types.
- `repository/postgres_pull_requests.go` — `SavePullRequest` only.
- `activities/github/` — GitHub App client + ETL activities.
- `workflows/github/extract.go` — ETL extract workflow.
- `cmd/worker/main.go` — worker registration.

---

## Task 1 — DB migration: create `pull_request_enrichments` table

**File:** `db/migrations/007_pr_enrichment.up.sql`

```sql
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
```

Notes:
- No existing rows to backfill — `pull_request_enrichments` rows are created by `SavePullRequest` going forward.
- No down migration required in v1.

---

## Task 2 — Model: add enrichment types

**File:** `models/pull_request.go` (or new `models/pull_request_enrichment.go`)

Add new types:

```go
type PREnrichmentStatus string

const (
    PREnrichmentStatusPending    PREnrichmentStatus = "pending"
    PREnrichmentStatusInProgress PREnrichmentStatus = "in_progress"
    PREnrichmentStatusCompleted  PREnrichmentStatus = "completed"
    PREnrichmentStatusFailed     PREnrichmentStatus = "failed"
)

type PullRequestEnrichment struct {
    PullRequestKey uuid.UUID
    OrgID          uuid.UUID
    Status         PREnrichmentStatus
    Attempts       int
    Enrichment     *PullRequestEnrichmentData
    EnrichedAt     *time.Time
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

type PullRequestEnrichmentData struct {
    FeatureFlags PullRequestFeatureFlagRefs `json:"feature_flags"`
}

type PullRequestFeatureFlagRefs struct {
    Added   []string `json:"added"`
    Removed []string `json:"removed"`
}
```

`PullRequest` itself does not need new fields — enrichment is a separate model.

---

## Task 3 — Repository: enrichment methods

**File:** `repository/postgres_pull_request_enrichments.go` (new file)
**File:** `repository/repository.go`

Update `SavePullRequest` to also insert a `pull_request_enrichments` row in the same transaction. On conflict (re-ingest of existing PR), do nothing on the enrichment row — ETL must not overwrite enrichment state.

Add to `Repository` interface and `PostgresRepository`:

### `ListPendingEnrichments`

```go
ListPendingEnrichments(ctx context.Context, limit int) ([]models.PullRequestEnrichment, error)
```

Fetches up to `limit` enrichment rows with `status = 'pending'`, ordered by `created_at ASC`.

### `ClaimEnrichment`

```go
ClaimEnrichment(ctx context.Context, pullRequestKey uuid.UUID) error
```

Sets `status = 'in_progress'`, increments `attempts`. No-op if row is already `in_progress` or `completed`.

### `SaveEnrichmentResult`

```go
SaveEnrichmentResult(ctx context.Context, pullRequestKey uuid.UUID, data models.PullRequestEnrichmentData) error
```

Writes `enrichment` JSONB, sets `enriched_at = now()`, sets `status = 'completed'`.

### `MarkEnrichmentFailed`

```go
MarkEnrichmentFailed(ctx context.Context, pullRequestKey uuid.UUID) error
```

If `attempts >= 5`, sets `status = 'failed'`. Otherwise sets `status = 'pending'` (retry next run).

Add repository integration tests in `repository/postgres_pull_request_enrichments_test.go` covering:
- enrichment row created automatically on `SavePullRequest`
- re-ingest of existing PR does not reset enrichment state
- status transitions: pending → in_progress → completed
- retry below cap: failed → pending
- retry cap reached: failed → failed

---

## Task 4 — Check in LaunchDarkly reference code

**Directory:** `internal/reference/launchdarkly-find-code-refs/` (or similar)

Temporarily check in the relevant source files from [launchdarkly/find-code-references-in-pull-request](https://github.com/launchdarkly/find-code-references-in-pull-request) as a read-only reference.

Files of interest:
- `diff/diff.go` — `PreprocessDiffs`, `ProcessDiffs`
- `search/search.go` — `ElementMatcher`, `FindMatches`
- `internal/utils/diff_util/operation.go` — line operation types
- `internal/references/builder.go` — `ReferenceSummary`, `FlagsAdded`, `FlagsRemoved`

Add a `README` or header comment in the directory noting that this is temporary reference material and must be removed before the enrichment feature ships.

---

## Task 5 — Diff fetch activity

**File:** `activities/github/fetch_pr_diff.go`

Add `FetchPRDiff` activity to `GitHubActivities`:

```go
func (a *GitHubActivities) FetchPRDiff(ctx context.Context, input FetchPRDiffInput) (string, error)
```

Input: `IntegrationID`, `OrgID`, `Owner`, `Repo`, `PRNumber`, `BaseRef`, `HeadSHA`

Behavior:
1. Mint an installation access token for the integration.
2. Call `GET /repos/{owner}/{repo}/pulls/{number}` with `Accept: application/vnd.github.v3.diff`.
3. On HTTP 406 (diff too large): fall back to `GET /repos/{owner}/{repo}/compare/{base}...{head}` and reconstruct a unified diff from per-file patches.
4. Return the raw unified diff string.

Add unit tests in `activities/github/fetch_pr_diff_test.go`.

---

## Task 6 — Flag-finding logic

**File:** `activities/github/scan_diff.go`

Implement diff scanning, porting the LaunchDarkly approach:

```go
func ScanDiffForFlags(rawDiff string, flagKeys []string) (ScanResult, error)

type ScanResult struct {
    Added   []string // flag keys found on "+" lines
    Removed []string // flag keys found only on "-" lines
}
```

Steps inside `ScanDiffForFlags`:
1. Parse the raw unified diff into per-file hunks using `github.com/sourcegraph/go-diff/diff`.
2. For each file, skip dotfiles and pure renames.
3. For each hunk line, classify by first character (`+` / `-` / context).
4. For added and removed lines, run delimiter-aware substring search for each flag key.
5. A flag on both `+` and `-` lines → classified as added.
6. A flag only on `-` lines → classified as removed.

Add unit tests in `activities/github/scan_diff_test.go` with synthetic diffs covering:
- flag on added line only
- flag on removed line only
- flag on both (modified)
- no flags
- dotfile skipped
- pure rename skipped

---

## Task 7 — Enrichment activity

**File:** `activities/github/enrich_pull_request.go`

Add `EnrichPullRequest` activity:

```go
func (a *GitHubActivities) EnrichPullRequest(ctx context.Context, input EnrichPRInput) (models.PullRequestEnrichmentData, error)
```

Input: `PullRequestEnrichment` (full enrichment row including PR key, org, integration), `FlagKeys []string`

Steps:
1. Call `FetchPRDiff` to get raw diff.
2. Call `ScanDiffForFlags` with the diff and flag keys.
3. Resolve matched flag key strings to DataGill flag UUIDs (query `feature_flags` table).
4. Return `PullRequestEnrichmentData` with `Added` and `Removed` UUID slices.

The repository query for flag UUID resolution needs a new method — add `GetFeatureFlagsByKeys(ctx, orgID, keys) ([]models.FeatureFlag, error)` to `repository.go` and implement in `postgres_feature_flags.go`.

Add unit tests in `activities/github/enrich_pull_request_test.go`.

---

## Task 8 — Enrichment workflow

**File:** `workflows/github/enrichment.go`

Add `GitHubEnrichmentWorkflow`:

```go
func GitHubEnrichmentWorkflow(ctx workflow.Context) error
```

No input — runs globally across all orgs.

Steps:
1. Call `ListPendingEnrichments` activity (batch of 100).
2. If batch is empty, return.
3. For each enrichment row in the batch:
   a. Call `ClaimEnrichment`.
   b. Load the org's flag keys via `ListFeatureFlagKeys` activity.
   c. Call `EnrichPullRequest`.
   d. On success: call `SaveEnrichmentResult`.
   e. On failure: call `MarkEnrichmentFailed`.
4. Process sequentially in v1. Fan-out can be added later if throughput requires it.

Activity options:
- `StartToCloseTimeout`: 30s per PR
- Temporal retries handle transient GitHub errors (rate limits, network)

Add workflow tests in `workflows/github/enrichment_test.go` covering:
- empty batch (no-op)
- successful enrichment
- failed enrichment below retry cap → reset to `pending`
- failed enrichment at retry cap → set to `failed`

---

## Task 9 — Worker registration

**File:** `cmd/worker/main.go`

Register `GitHubEnrichmentWorkflow` alongside the existing GitHub workflows.

No new activity registration needed — `EnrichPullRequest`, `FetchPRDiff`, and the new repository activities are methods on `GitHubActivities` and `SharedActivities`, which are already registered.

---

## Task 10 — Temporal schedule

**File:** `services/search/scheduler.go` (or equivalent schedule setup)

Create a Temporal schedule for the enrichment workflow:

- Schedule ID: `github-pr-enrichment`
- Workflow: `GitHubEnrichmentWorkflow`
- Cadence: every 30 minutes
- No per-org input — the workflow queries pending rows globally

The schedule should be created once at startup, idempotent (use `CreateSchedule` with conflict policy `Skip`).

---

## Task 11 — Remove reference code

**Directory:** `internal/reference/launchdarkly-find-code-refs/`

Delete the temporary reference directory after Tasks 5–7 are complete and reviewed.

This should land as its own PR so the removal is clearly intentional.

---

## Recommended PR Breakdown

| PR | Tasks | Description |
|----|-------|-------------|
| 8A | 1, 2 | Migration + enrichment model |
| 8B | 3 | Repository enrichment methods + tests |
| 8C | 4 | Check in LD reference code |
| 8D | 5, 6 | Diff fetch + flag scan activities + tests |
| 8E | 7 | `EnrichPullRequest` activity + flag UUID resolution |
| 8F | 8, 9, 10 | Enrichment workflow + worker registration + schedule |
| 8G | 11 | Remove LD reference code |

---

## Risks to Watch

- **Flag key resolution**: flag keys in code may be string literals that match the `key` field on `feature_flags`, not the UUID. Confirm the match field before Task 7.
- **Diff 406 fallback**: the compare API fallback for oversized diffs has not been tested against a real 406 case. Validate early in Task 5.
- **`SavePullRequest` transaction**: the enrichment row insert must be in the same transaction as the PR insert. If they split, a PR can exist without an enrichment row and be silently skipped.
- **Sequential enrichment throughput**: 100 PRs × 2 GitHub API calls each = up to 200 API calls per 30-minute run. Installation token rate limits are 5,000 requests/hour — well within budget for early usage, but watch as PR volume grows.
