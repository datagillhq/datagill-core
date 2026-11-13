# GitHub PR Enrichment Design

> Status: In Design — flag-finding matching delimiters and re-enrichment strategy deferred to implementation.
> Companion to [github-integration.md](./github-integration.md).

---

## Overview

After GitHub ETL ingests merged pull requests into `pull_requests`, a separate enrichment workflow processes each PR to extract additional signal from the diff. The first enrichment type is **LaunchDarkly feature flag association**: scanning PR code for known flag keys and recording which flags appear in the change.

The flag-finding logic replicates [launchdarkly/find-code-references-in-pull-request](https://github.com/launchdarkly/find-code-references-in-pull-request). The implementation is a Go re-implementation of that action's core diff-scanning and flag-matching approach, adapted for Temporal activities instead of a GitHub Actions runner.

The LaunchDarkly source will be temporarily checked into this repo as a reference while the logic is being ported. It should be removed once the re-implementation is complete.

More enrichment types (security signals, co-author detection, etc.) may be added to the same pipeline later without structural changes.

---

## Design Decisions

### Enrichment lives in a separate `pull_request_enrichments` table

Enrichment data is stored in a dedicated 1:1 table rather than as columns on `pull_requests`.

| Column | Type | Notes |
|--------|------|-------|
| `pull_request_key` | `uuid` PK | FK to `pull_requests.key`. |
| `org_id` | `uuid` | Denormalized for query convenience. |
| `status` | `text` | Tracks enrichment lifecycle. See status values below. |
| `attempts` | `int` | Incremented on each enrichment attempt. Used to cap retries. |
| `next_attempt_at` | `timestamptz` | When a `pending` row may next be claimed. Defaults to `NOW()`; pushed out with backoff after each failure. Added in `009_pr_enrichment_next_attempt_at.up.sql`. |
| `enrichment` | `jsonb` | Computed enrichment output. Null until first enrichment run. |
| `enriched_at` | `timestamptz` | Set when enrichment completes successfully. Null = not yet enriched. |
| `created_at` | `timestamptz` | |
| `updated_at` | `timestamptz` | |

**Why a separate table:**
- `status` and `attempts` are worker/processing state, not PR metadata — they don't belong on the PR row.
- `pull_requests` stays clean and owned entirely by ETL. No mixing of concerns between the ETL writer and the enrichment worker.
- Enrichment can be reset or dropped without touching the PR row.
- `pull_requests` already has a `state` column (merged/closed); adding a `status` column for enrichment on the same table is confusing.
- Future enrichment types can be added as columns on `pull_request_enrichments` without touching the ETL table.

**Row creation:** `SavePullRequest` inserts a corresponding `pull_request_enrichments` row with `status = 'pending'` in the same transaction. ETL owns the insert; the enrichment worker only ever updates.

**Why `enriched_at` is a real column, not inside JSONB:** The enrichment workflow queries for pending rows with `WHERE status = 'pending'`. A real column supports a clean B-tree index; a JSONB path cannot.

### Enrichment status values

| Value | Meaning |
|-------|---------|
| `pending` | Not yet enriched, or waiting to retry. Default on insert. Claimable once `next_attempt_at` has passed. |
| `in_progress` | Claimed by a workflow run. Treated as abandoned, and claimable again, if not updated for 1 hour. |
| `completed` | Enrichment finished successfully. |
| `failed` | Enrichment exceeded the retry cap. Will not be retried automatically. |

Retries happen at two levels:

1. **Within a run:** Temporal retries each activity up to 3 attempts to absorb transient errors (rate limits, network). The bound matters: Temporal's default is unlimited retries, which would leave a persistently failing PR `in_progress` forever and block the rest of the batch. Each activity also has a total time limit (its 3 attempts plus 5 minutes of retry waits), so a long rate-limit wait ends the activity instead of keeping the PR `in_progress` past the 1-hour stale window. Worst case per PR is under 40 minutes.
2. **Across runs, with backoff:** once Temporal's attempts are exhausted, the PR is marked failed for this run. The `attempts` column is incremented on each claim. Below the cap (10) the row returns to `pending` with `next_attempt_at` pushed out by a wait that starts at 30m and doubles per failure, capped at 24h: 30m, 1h, 2h, 4h, 8h, 16h, 24h, 24h, 24h. These are minimum waits: the retry happens on the first scheduled run (every 30 minutes) after the wait, so in practice each is up to one run longer. At the cap, about 4.3 days after the first failure, status is set to `failed`. The values are Go constants in `repository/postgres_pull_request_enrichments.go`.

Activity errors are classified by a worker interceptor (`activities.ErrorClassificationInterceptor`). Permanent errors (401 bad credentials, 404 missing installation or PR) fail the activity on the first try instead of using up Temporal's in-run retries; rate limits keep their `Retry-After` delay. GitHub's 403 rate-limit responses (`x-ratelimit-remaining: 0`, or `Retry-After` on a secondary limit) are treated as rate limits, not as bad credentials. Permanent errors still go through the cross-run backoff: a 401 usually means the integration's credentials are bad rather than the PR, and failing PRs at once would lose them if the user fixes the credentials. The backoff is what lets a multi-day GitHub outage pass without permanently failing every pending PR.

### `enrichment` JSONB shape

```json
{
  "feature_flags": {
    "added": ["flag-uuid-1", "flag-uuid-2"],
    "removed": ["flag-uuid-3"]
  }
}
```

- `added`: flag keys found on `+` lines in the diff (introduced or modified).
- `removed`: flag keys found only on `-` lines (removed and no longer present in `+` lines).
- A flag on both `+` and `-` lines is classified as `added` (i.e., modified).

Flag values are DataGill flag UUIDs, resolved from the `feature_flags` table at enrichment time.

Future enrichment types add keys under the same column. No schema migration required unless a new key needs its own index.

### GIN index for flag lookups

```sql
CREATE INDEX idx_pr_enrichment_flags_added ON pull_request_enrichments USING GIN ((enrichment->'feature_flags'->'added'));
CREATE INDEX idx_pr_enrichment_flags_removed ON pull_request_enrichments USING GIN ((enrichment->'feature_flags'->'removed'));
```

Enables the query "which PRs reference this flag?":

```sql
SELECT pr.*
FROM pull_requests pr
JOIN pull_request_enrichments e ON e.pull_request_key = pr.key
WHERE e.org_id = 'org-uuid'
AND e.enrichment->'feature_flags'->'added' @> '"flag-uuid-1"';
```

---

## Flag Key Source

Flag keys are loaded from the `feature_flags` table for the PR's `org_id` at enrichment time. No cache layer in v1 — queried fresh per PR batch.

Only flags present in the org's `feature_flags` table at enrichment time are matched. **Flags created after a PR is merged will not be retroactively associated with that PR.** This is an accepted v1 limitation — see Deferred Decisions below.

---

## Diff Fetch

Replicates the approach from `launchdarkly/find-code-references-in-pull-request`:

1. **Primary path**: Call `GET /repos/{owner}/{repo}/pulls/{pull_number}` with `Accept: application/vnd.github.v3.diff` to retrieve the raw unified diff. The `diff_url` stored in `pull_requests.data.details.diff.url` is used to identify the PR.

2. **Fallback (HTTP 406 — diff too large)**: Call `GET /repos/{owner}/{repo}/compare/{base}...{head}` to get the merge-base SHA, then call `GET /repos/{owner}/{repo}/git/commits/{sha}` for the head SHA, and construct the diff from per-file patch content. In the GitHub Actions version this falls back to `git diff` CLI; in DataGill's worker this fallback uses the GitHub compare API instead since there is no local git checkout.

The raw diff is parsed as a unified multi-file diff. Each `FileDiff` is processed independently.

### File filtering

Skip a file if:
- It is a dotfile (filename starts with `.`).
- It is a pure rename with no content change.

### Line scanning

For each file's hunk content:
- Split by `"\n"`.
- Classify each line by its first character:
  - `+` → added line
  - `-` → removed line
  - anything else → context line (skip)
- Run flag-key matching against added and removed lines only.

### Flag-key matching

Delimiter-aware substring search, replicating `ld-find-code-refs` `ElementMatcher` behavior:
- Check whether the flag key appears as a substring of the line.
- Optionally, if delimiters are configured, require the key to be surrounded by a delimiter character (e.g., `"`, `'`, `` ` ``).
- v1 delimiter configuration: TBD at implementation time.

### Large diff handling

No explicit byte-size truncation in application code. GitHub API limits apply:
- If GitHub returns HTTP 406, the fallback path is used.
- No per-file or per-flag hard cap is applied in v1 beyond what the GitHub API enforces.

---

## Enrichment Workflow

A separate Temporal schedule per org, independent of the ETL sync workflow. It does not run as part of `FeatureFlagETLWorkflow` or `GitHubExtractWorkflow`.

**Schedule cadence: every 30 minutes, per org.**

- Schedule ID `github-pr-enrichment-{orgID}`, created when the org's first GitHub integration is connected. Connecting a second GitHub integration in the same org, or reconnecting after a failed connect, reuses the existing schedule.
- Each org gets a stable offset within the 30 minutes, hashed from its org ID. Temporal interval schedules fire at fixed clock times (not relative to the last run), so the offset keeps orgs from all running at :00 and :30.
- Per-org scheduling isolates orgs from each other: one org's failures or GitHub rate limits (which are per installation) do not delay another org's enrichment. Temporal schedules are cheap, so one per org costs about the same as one global schedule.
- Each run processes one batch and completes. The schedule owns the cadence; the workflow does not sleep or continue-as-new. Overlap policy `SKIP` prevents two runs for the same org at once.
- The schedule is not removed when GitHub is paused or disconnected. Each run only lists PRs whose integration is `active`, so PRs of a paused or deleted integration stay `pending` without calling GitHub, and are picked up again once the integration is active. A run with nothing to do finishes immediately.

### Batch query

Each run fetches a bounded batch of claimable enrichment rows for its org: pending rows, plus `in_progress` rows whose claim is stale (not updated for 1 hour, e.g. the worker died mid-run).

```sql
SELECT pull_request_key, org_id, status, attempts, enrichment, enriched_at, created_at, updated_at
FROM pull_request_enrichments
WHERE org_id = $1
  AND ((status = 'pending' AND next_attempt_at <= NOW())
       OR (status = 'in_progress' AND updated_at < NOW() - INTERVAL '1 hour'))
  AND EXISTS (SELECT 1 FROM pull_requests pr JOIN integrations i ON i.key = pr.integration_id
              WHERE pr.key = pull_request_enrichments.pull_request_key AND i.state = 'active')
ORDER BY created_at ASC
LIMIT 100;
```

The org's flag keys are loaded once per batch. PRs in the batch are processed in parallel, 10 at a time.

Each PR is claimed before processing: the claim sets `status = 'in_progress'` and increments `attempts`, and reports whether it actually claimed the row. If not (another run holds it, or it already finished), the PR is skipped. Writing the result and marking failure only update rows that are still `in_progress`, so a stale or duplicate run cannot overwrite a finished row. On success, `enriched_at` and `enrichment` are written and status is set to `completed`. On failure, if attempts reached the cap, status is set to `failed`; otherwise status is reset to `pending` and `next_attempt_at` is set by the backoff schedule.

The status condition is the cursor. No cursor state is stored in Temporal.

### Per-PR enrichment steps

1. Claim the row: set `status = 'in_progress'`, increment `attempts`. Skip the PR if the claim was not acquired.
2. Use the org's flag keys, loaded once for the batch.
3. Fetch the raw unified diff using the installation access token.
4. Parse and scan the diff for flag key matches.
5. Resolve matched flag keys to DataGill flag UUIDs.
6. Write `enrichment` JSONB and set `enriched_at`, `status = 'completed'` on the enrichment row.

### Auth

Uses the same installation-token auth as the ETL worker. The activity fetches a short-lived installation access token at execution time using the integration's `installation_id`. Token is not stored.

---

## DB Migration

New table (separate migration from `006_pull_requests.up.sql`):

```sql
CREATE TABLE pull_request_enrichments (
    pull_request_key UUID        PRIMARY KEY REFERENCES pull_requests(key) ON DELETE CASCADE,
    org_id           UUID        NOT NULL REFERENCES organizations(key) ON DELETE CASCADE,
    status           TEXT        NOT NULL DEFAULT 'pending',
    attempts         INT         NOT NULL DEFAULT 0,
    enrichment       JSONB,
    enriched_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()  -- added in 009_pr_enrichment_next_attempt_at.up.sql
);

CREATE INDEX idx_pr_enrichments_status ON pull_request_enrichments(status)
  WHERE status = 'pending';

CREATE INDEX idx_pr_enrichments_org_id ON pull_request_enrichments(org_id);

CREATE INDEX idx_pr_enrichment_flags_added ON pull_request_enrichments
  USING GIN ((enrichment->'feature_flags'->'added'));

CREATE INDEX idx_pr_enrichment_flags_removed ON pull_request_enrichments
  USING GIN ((enrichment->'feature_flags'->'removed'));
```

---

## Deferred Decisions

| Decision | Notes |
|----------|-------|
| Re-enrichment for new flags | Flags created after a PR is merged will not be associated with that PR in v1. Re-enrichment triggered by flag creation events is deferred. If needed, a backfill job can reset `pull_request_enrichments.status = 'pending'` for PRs merged within a relevant window. |
| Matching delimiter configuration | The exact delimiter set used for flag-key matching is TBD at implementation time. Start with no delimiter requirement and tighten if false-positive rates are high. |
| Backfill enrichment schedules | Orgs that connected GitHub before per-org schedules shipped have no enrichment schedule. A one-time script is needed only if such orgs exist outside local development. |
| Failed PR manual retry | v1 has no API endpoint to manually reset a `failed` PR back to `pending`. Ops workaround is a direct SQL update. |

---

## Open Questions

| Question | Notes |
|----------|-------|
| Diff fetch fallback in worker | The GitHub Actions version falls back to `git diff` CLI when GitHub returns 406. The worker has no local git checkout. The fallback path using GitHub compare API needs to be validated against real 406-triggering PRs. |
| Error handling for permanently unavailable diffs | If GitHub returns 404 for a diff (deleted repo, private fork), should the PR be marked `failed` immediately (no retry) or treated as a transient error? Likely `failed` after 1 attempt, but not yet decided. |
