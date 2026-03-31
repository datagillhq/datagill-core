# ⚠️ Removed from MVP

The Feature Flag Audit Detail page has been removed from scope. DataGill is a business intelligence tool — for flag-level detail, users are linked directly to their source provider (LaunchDarkly, etc.).

Flag-level analytics (`GET /v1/flags/{key}/analytics?metric=...`) may be revisited in a future phase and will share the same metric namespace as `GET /v1/analytics`.

The content below is preserved for reference only.

---

# Backend: Flag Audit Detail Page — API Design Context

## Overview

This document captures the API design research done for the **Feature Flag Audit History Detail** page. It covers the current state of the backend, what gaps exist, and what new endpoints and data model changes are needed to support the frontend mock.

Use this as context when creating the implementation plan — do **not** start implementing without reading this first and aligning on scope.

---

## Current API Surface (Search Service)

All endpoints live in `services/search/`. All require `X-Org-Id` header (set by API gateway after JWT verification).

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/flags` | List all flags for org, with optional `?source=` and `?environment=` filters |
| `GET` | `/flags/{flag_key}` | Full flag detail: name, description, tags, createdAt, updatedAt |
| `GET` | `/flags/{flag_key}/history` | Paginated changelog for a flag (`?page=`, `?limit=`) |
| `GET` | `/search` | Full-text search across flag names |
| `GET` | `/timeline` | Org-wide changelog across all flags |

Routing is defined in `services/search/server.go`. Handlers are in `services/search/handlers.go`. Repository queries are in `repository/`.

---

## Key Data Models

### `FeatureFlag` (`models/canonical.go`)
Stores normalized flag state. The `Data` field is a JSONB blob containing:
- `Name`, `Description`
- `Source` (provider key, provider name, project ID)
- `Maintainer` (person or team)
- `Lifecycle` (purpose: temporary|permanent, state: active|stale|dead|archived|deprecated)
- `Environments` — map of environment name → `FlagEnvironment` (enabled bool per env)

### `FeatureFlagChangelog` (`models/canonical.go`)
Stores normalized change events. The `Data` field (`ChangelogData`) contains:
- `actor` — **email only** (no display name, no avatar)
- `environments` — list of affected environment names
- `description` — free-text description of the change
- `change_type` — coarse categorization

### `ChangelogEntry` DTO (`services/search/types.go`)
What the API actually returns from `/history` and `/timeline`. Maps from `FeatureFlagChangelog`.

---

## Gaps: Existing Endpoints vs. What the Mock Needs

### 1. `GET /flags/{flag_key}` — mostly fine
- Returns name, status, createdAt, updatedAt ✅
- Does **not** return who last modified it (that requires the most recent changelog entry)

### 2. `GET /flags/{flag_key}/history` — needs extensions
- Missing: `total` count in response (current `count` field is page-level only)
- Missing: date range filter (`?from=`, `?to=`, or `?days=N`)
- Pagination stays page-based for now (cursor-based is a future improvement)

### 3. No aggregation endpoints exist
The mock requires aggregated views of changelog data. None of these exist:
- Count of total mutations for a flag
- Count of mutations grouped by actor
- Count of mutations grouped by day

---

## New Endpoints Required

### `GET /flags/{flag_key}/stats`
Returns the lifecycle summary card data.

```json
{
  "orgId": "...",
  "flagKey": "...",
  "createdAt": "2023-09-12T00:00:00Z",
  "totalMutations": 156,
  "productionRollout": 15
}
```

**Notes:**
- `totalMutations` = `COUNT(*)` on `feature_flag_changelog` where `feature_flag_key = ?`
- `productionRollout` = rollout percentage for the production environment. This lives in the raw provider payload. It is **not currently stored** in `FlagEnvironment` (see data model gap below).
- `createdAt` is already on `FeatureFlag` — included here to avoid a second round-trip from the frontend.

### `GET /flags/{flag_key}/contributors?days=30`
Returns contributor insights card data.

```json
{
  "orgId": "...",
  "flagKey": "...",
  "windowDays": 30,
  "contributors": [
    { "actorEmail": "sarah@acme.com", "actorName": "Sarah Jenkins", "changes": 64 },
    { "actorEmail": "david@acme.com", "actorName": "David Chen", "changes": 42 }
  ]
}
```

**Notes:**
- Query: `GROUP BY actor_email, actor_name` with `WHERE occurred_at > NOW() - INTERVAL '30 days'`
- `actorName` requires a data model change (see below) — currently we only store email
- Results ordered by `changes DESC`
- The frontend always uses 30-day window; no timeframe toggle

### `GET /flags/{flag_key}/activity?days=30`
Returns daily change counts. Used by **both** the activity calendar heatmap and the change frequency line chart — same data, two visualizations.

```json
{
  "orgId": "...",
  "flagKey": "...",
  "windowDays": 30,
  "activity": [
    { "date": "2023-10-01", "count": 3 },
    { "date": "2023-10-02", "count": 0 },
    ...
  ]
}
```

**Notes:**
- Query: `GROUP BY date_trunc('day', occurred_at)` with date range filter
- Must return entries for all days in the window, including zeros (fill gaps in application layer or SQL)
- Change frequency graph release cycle annotations (`v2.4 Release`) are **out of scope** — no release data source exists

---

## Data Model Changes Required

These are changes to what gets stored during ETL normalization, not just API shape.

### 1. `ChangelogData` — add `actor_name`
LaunchDarkly's audit log API returns the member's display name. We currently only capture email. Add `ActorName string` to `ChangelogData` and populate it during LaunchDarkly activity normalization.

Location to change: `activities/launchdarkly/` (normalization logic) and `models/canonical.go`.

### 2. `ChangelogData` — add `diff`
The audit log detail entries in the mock show before/after diffs (`Rollout: 10% → Rollout: 15%`, `ENABLED: true → false`). LaunchDarkly's API includes a `changes[]` array with before/after per attribute on each audit log entry. We currently discard this.

Add a `Diff` field to `ChangelogData`:
```go
type ChangelogDiff struct {
    Attribute string `json:"attribute"`
    OldValue  any    `json:"old_value"`
    NewValue  any    `json:"new_value"`
}
```

This is the **most significant** data model change. Raw payloads are preserved in `source_records` so backfilling is theoretically possible, but non-trivial.

### 3. `FlagEnvironment` — add `rollout_percentage`
`FlagEnvironment` currently only stores a boolean `enabled` state. LaunchDarkly's targeting rules include a rollout percentage. Add `RolloutPercentage *float64` (nullable — not all flags have percentage rollouts).

### 4. `ChangelogData` — add `action_label`
The mock shows fine-grained action labels like "Target Rule Updated", "Killswitch Engaged", "Metadata Update". Our current `eventType` enum only has 5 coarse values (`created`, `updated`, `toggled`, `deleted`, `rollback`). LaunchDarkly's audit log has a `title` field per entry.

Add `ActionLabel string` to `ChangelogData` and populate from LD's `title` field.

---

## Database Considerations

### Existing indexes (already cover all new queries)
```sql
-- On feature_flag_changelog:
(feature_flag_key, occurred_at)  -- covers all aggregation queries scoped to a flag
(org_id, occurred_at)            -- covers timeline queries
```

No new indexes are needed for the new endpoints. All aggregation queries (`/stats`, `/contributors`, `/activity`) filter on `feature_flag_key` and optionally `occurred_at`, both of which are covered.

### Potential future concern: `totalMutations` COUNT at scale
`SELECT COUNT(*)` on a large changelog is fast with the index but can drift at very high volume. If it becomes a bottleneck, the fix is to add a `mutation_count` counter column on `feature_flags` and increment it on each changelog insert. Do not preemptively optimize.

---

## Auth

The frontend sends a JWT. The API gateway validates it and injects `X-Org-Id`. The search service trusts this header — no changes needed to auth for the new endpoints. All new endpoints follow the same pattern as existing ones.

---

## Implementation Order Recommendation

When creating the implementation plan, consider this ordering:

1. **Data model changes first** (`actor_name`, `action_label`, `diff`, `rollout_percentage`) — these need to land before the ETL re-processes data, and are prerequisite to the API returning useful data
2. **Extend `/history` response** — add `total` field, add date range filter
3. **New aggregation endpoints** — `/stats`, `/contributors`, `/activity` — these are pure read-path additions, no model changes required beyond #1
4. **New database migration** if any schema changes are needed for the diff storage

Each step should be independently testable. Integration tests use real Postgres via testcontainers (see project testing philosophy).
