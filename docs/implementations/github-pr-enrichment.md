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

### Enrichment lives on `pull_requests` directly

Rather than a separate table, new columns are added to `pull_requests`:

| Column | Type | Notes |
|--------|------|-------|
| `enrichment` | `jsonb` | Computed enrichment output. Null until first enrichment run. |
| `enriched_at` | `timestamptz` | Set when enrichment completes successfully. Null = not yet enriched. |
| `status` | `text` | Tracks enrichment lifecycle. See status values below. |
| `attempts` | `int` | Incremented on each enrichment attempt. Used to cap retries. |

**Why not a separate table:** No join needed for reads. ETL owns `data`, enrichment owns `enrichment`. Clear column-level ownership with no table proliferation.

**Why `enriched_at` is a real column, not inside JSONB:** The enrichment workflow queries for unenriched PRs with `WHERE status = 'pending'`. A real column supports a clean B-tree index; a JSONB path cannot.

### Enrichment status values

| Value | Meaning |
|-------|---------|
| `pending` | Not yet enriched. Default on insert. |
| `in_progress` | Currently being processed by a Temporal activity. |
| `completed` | Enrichment finished successfully. |
| `failed` | Enrichment exceeded the retry cap. Will not be retried automatically. |

Temporal activity-level retries handle transient errors (rate limits, network). The `attempts` column is incremented on each attempt. Once it exceeds the retry cap (v1: 5), status is set to `failed`.

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
CREATE INDEX idx_pr_enrichment_flags_added ON pull_requests USING GIN ((enrichment->'feature_flags'->'added'));
CREATE INDEX idx_pr_enrichment_flags_removed ON pull_requests USING GIN ((enrichment->'feature_flags'->'removed'));
```

Enables the query "which PRs reference this flag?":

```sql
SELECT * FROM pull_requests
WHERE org_id = 'org-uuid'
AND enrichment->'feature_flags'->'added' @> '"flag-uuid-1"';
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

A completely separate Temporal schedule, independent of the ETL sync workflow. It does not run as part of `FeatureFlagETLWorkflow` or `GitHubExtractWorkflow`.

**Schedule cadence: every 30 minutes.**

### Batch query

Each run fetches a bounded batch of pending PRs:

```sql
SELECT * FROM pull_requests
WHERE status = 'pending'
ORDER BY created_at ASC
LIMIT 100;
```

Status is set to `in_progress` before processing begins. On success, `enriched_at` and `enrichment` are written and status is set to `completed`. On failure, `attempts` is incremented; if attempts exceed the cap, status is set to `failed`; otherwise status is reset to `pending` for the next run.

The `status = 'pending'` condition is the cursor. No cursor state is stored in Temporal.

### Per-PR enrichment steps

1. Set `status = 'in_progress'`, increment `attempts`.
2. Load the org's flag keys from `feature_flags` for the PR's `org_id`.
3. Fetch the raw unified diff using the installation access token.
4. Parse and scan the diff for flag key matches.
5. Resolve matched flag keys to DataGill flag UUIDs.
6. Write `enrichment` JSONB and set `enriched_at`, `status = 'completed'`.

### Auth

Uses the same installation-token auth as the ETL worker. The activity fetches a short-lived installation access token at execution time using the integration's `installation_id`. Token is not stored.

---

## DB Migration

New columns on `pull_requests` (separate migration from `006_pull_requests.up.sql`):

```sql
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
```

---

## Deferred Decisions

| Decision | Notes |
|----------|-------|
| Re-enrichment for new flags | Flags created after a PR is merged will not be associated with that PR in v1. Re-enrichment triggered by flag creation events is deferred. If needed, a backfill job can reset `enrichment_status = 'pending'` for PRs merged within a relevant window. |
| Matching delimiter configuration | The exact delimiter set used for flag-key matching is TBD at implementation time. Start with no delimiter requirement and tighten if false-positive rates are high. |
| Enrichment schedule per org vs. global | v1 runs a single global enrichment schedule across all orgs. Per-org scheduling is deferred. |
| Failed PR manual retry | v1 has no API endpoint to manually reset a `failed` PR back to `pending`. Ops workaround is a direct SQL update. |

---

## Open Questions

| Question | Notes |
|----------|-------|
| Diff fetch fallback in worker | The GitHub Actions version falls back to `git diff` CLI when GitHub returns 406. The worker has no local git checkout. The fallback path using GitHub compare API needs to be validated against real 406-triggering PRs. |
| Error handling for permanently unavailable diffs | If GitHub returns 404 for a diff (deleted repo, private fork), should the PR be marked `failed` immediately (no retry) or treated as a transient error? Likely `failed` after 1 attempt, but not yet decided. |
