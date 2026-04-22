# Backend: Analytics API — Design Context

## Overview

This document covers the `GET /v1/analytics` endpoint, which powers the Executive Dashboard. It replaces the old `/dashboard/*` endpoints.

All analytics are served through a single versioned endpoint with a `metric` query parameter. The backend uses a **plugin/decorator architecture** — each metric is a self-contained registered plugin. The handler routes the `metric` param to the right plugin. Adding a new metric means registering a new plugin, with no changes to the routing layer.

Read alongside `docs/frontend/dashboard-page.md` before creating an implementation plan.

---

## Authentication & Trust Model

Same model as all DataGill backend services — see `docs/backend/integration-management-api.md` for the full breakdown.

In short: DataGill validates JWTs directly in middleware using BetterAuth's public key (fetched from its JWKS endpoint). The `organizationId` claim is extracted from the validated token and injected into the request context. All analytics queries are scoped to this org ID — clients cannot query another org's data.

---

## Existing Endpoints to Delete

The following endpoints exist in the current codebase (`services/search/`) and **must be deleted** as part of implementing the analytics API. They are fully replaced by `GET /v1/analytics`. Do not keep them for backwards compatibility — nothing depends on them yet.

| Method | Path | Status |
|--------|------|--------|
| `GET` | `/search` | Delete — replaced by `GET /v1/analytics?metric=audit_log` |
| `GET` | `/flags` | Delete — no replacement (flag list page removed from product) |
| `GET` | `/flags/{flag_key}` | Delete — users link to source provider directly via `sourceUrl` |
| `GET` | `/flags/{flag_key}/history` | Delete — replaced by `GET /v1/analytics?metric=audit_log` |
| `GET` | `/timeline` | Delete — replaced by `GET /v1/analytics?metric=audit_log` |

The `X-Org-Id` header middleware in `services/search/` is also replaced by the JWT middleware documented in `docs/backend/integration-management-api.md`.

---

## Endpoint

```
GET /v1/analytics?metric=<metric_name>&from=<date>&to=<date>
```

### Shared Query Parameters

| Param | Required | Description |
|---|---|---|
| `metric` | Yes | Which metric to compute — see supported metrics below |
| `from` | Yes | Start of timeframe (ISO date) |
| `to` | Yes | End of timeframe (ISO date) |
| `team` | No | Filter to flags owned by this team name |
| `actor` | No | Filter to changelog entries by this actor email |

`team` and `actor` filters AND together when both are present.

### Error Format

```json
{ "code": "ANALYTICS_UNKNOWN_METRIC" }
```

---

## Plugin Architecture

The analytics handler maintains a registry of metric plugins. Each plugin:
- Declares which `metric` name it handles
- Receives the parsed query params (from, to, team, actor, plus any metric-specific params)
- Executes its own DB query asynchronously
- Returns its portion of the response

When a request arrives, the handler looks up the plugin by `metric` name, executes it, and returns the result. Future support for multi-metric requests (`?metrics=a,b,c`) would run matching plugins concurrently and merge results.

This pattern keeps metric implementations isolated and independently testable.

---

## Supported Metrics

### `metric=launched_wins`

Flags that reached a **non-zero production rollout** within the timeframe.

- "Launched" = `rollout_percentage > 0` in the production environment
- 1–99% = partial rollout
- 0% = off (excluded)
- One row per flag — take the most recent qualifying production rollout event if multiple exist in the window
- Ordered by `occurredAt DESC`

**Prerequisite:** `rollout_percentage` in `ChangelogData` (see data model changes below).

**Response:**
```json
{
  "orgId": "...",
  "metric": "launched_wins",
  "from": "2024-05-06",
  "to": "2024-05-12",
  "data": [
    {
      "flagKey": "uuid",
      "flagName": "Apple Pay",
      "sourceUrl": "https://app.launchdarkly.com/...",
      "actorEmail": "marcus@acme.com",
      "actorName": "Marcus Chen",
      "rolloutPercentage": 100,
      "occurredAt": "2024-05-12T00:00:00Z"
    }
  ]
}
```

`sourceUrl` links back to the flag in the provider (LaunchDarkly, etc.) — derived from `integration.provider` + `flag.source.projectKey`.

---

### `metric=top_teams`

Teams ranked by number of changelog entries on flags they own within the timeframe.

**Team attribution:** based on `feature_flags.data.maintainer.team.name` (current owner). If ownership changed within the timeframe, historical changes are attributed to the new owner — this is acceptable.

**"Active Members"** = count of distinct actors in changelog entries for team-owned flags in the timeframe.

**Response:**
```json
{
  "orgId": "...",
  "metric": "top_teams",
  "from": "...",
  "to": "...",
  "data": [
    { "teamName": "Platform Eng", "changeCount": 842, "activeMembers": 8 },
    { "teamName": "Revenue Systems", "changeCount": 612, "activeMembers": 5 }
  ]
}
```

**Query approach:** JOIN `feature_flag_changelog` → `feature_flags` on `feature_flag_key`, GROUP BY `data->'maintainer'->'team'->>'name'`, filter by `occurred_at` range.

---

### `metric=velocity`

Average time from flag creation to 100% production rollout, for flags that completed rollout within the timeframe.

```
time_to_100 = timestamp_of_100pct_changelog_event − feature_flags.created_at
```

Only flags that **completed** rollout within the selected timeframe are included. `sampleSize` is returned so the frontend can show a disclaimer when the sample is too small (< 3).

**Prerequisite:** `rollout_percentage` in `ChangelogData`.

**Response:**
```json
{
  "orgId": "...",
  "metric": "velocity",
  "from": "...",
  "to": "...",
  "data": {
    "averageTimeToFullRolloutMinutes": 12.4,
    "sampleSize": 14
  }
}
```

---

### `metric=audit_log`

Raw changelog entries for the org within the timeframe, ordered by `occurredAt DESC`. Paginated. This is the persistent activity feed always shown at the bottom of the dashboard — it gives the user something meaningful to look at even when headline metrics (launched_wins, velocity) have no data for the period.

**Additional query params:**
| Param | Default | Description |
|---|---|---|
| `limit` | 25 | Entries per page (max 100) |
| `page` | 1 | Page number |

**Response:**
```json
{
  "orgId": "...",
  "metric": "audit_log",
  "from": "...",
  "to": "...",
  "total": 312,
  "page": 1,
  "count": 25,
  "data": [
    {
      "occurredAt": "2024-05-12T14:12:04Z",
      "flagName": "Apple Pay",
      "flagKey": "uuid",
      "sourceUrl": "https://app.launchdarkly.com/...",
      "actorEmail": "sarah@acme.com",
      "actorName": "Sarah Jenkins",
      "eventType": "toggled",
      "environments": ["production"]
    }
  ]
}
```

---

## Filter Dropdown Population

The dashboard has "Filter by Team" and "Filter by Person" dropdowns. Team names and actor emails are sourced from existing data — no SCIM or team management exists.

- **Filter by Team:** derive distinct `maintainer.team.name` values from `feature_flags` for the org. Can be served as a lightweight query on the flags table.
- **Filter by Person:** derive distinct `actor_email` values from `feature_flag_changelog` for the org.

These could be served by a utility endpoint or inlined into a metadata response. Decide during implementation.

---

## Data Model Changes Required

### `rollout_percentage *float64` on `FlagEnvironment`
Required for `launched_wins` and `velocity` metrics. LaunchDarkly's targeting rules include rollout percentage — add this field to `FlagEnvironment` and populate it during normalization.

### `actor_name string` on `ChangelogData`
Required for `launched_wins` actor display. LaunchDarkly's audit log returns display name — capture during normalization.

Both changes must land before the analytics endpoints return meaningful data for `launched_wins` and `velocity`.

---

## Database Considerations

### Existing indexes (cover all new queries)
- `(feature_flag_key, occurred_at)` on `feature_flag_changelog` — covers all date-range scans
- `(org_id, occurred_at)` on `feature_flag_changelog` — covers org-wide audit_log queries
- `key` on `feature_flags` — covers the changelog → flags join

### JSONB team filter
Querying `feature_flags.data->'maintainer'->'team'->>'name'` is a full column scan. If performance requires it, add a generated stored column:
```sql
ALTER TABLE feature_flags
  ADD COLUMN maintainer_team TEXT GENERATED ALWAYS AS (data->'maintainer'->'team'->>'name') STORED;
CREATE INDEX ON feature_flags (org_id, maintainer_team);
```
Do not add preemptively.

---

## Future

- `GET /v1/flags/{key}/analytics?metric=...` will share the same metric namespace
- AI summary (`metric=ai_summary`) is P1, not MVP — requires its own design doc covering caching and precompute strategy
- Multi-metric requests (`?metrics=a,b,c`) are supported by the plugin architecture and can be added without API changes

---

## Implementation Order Recommendation

1. **Data model changes** — `rollout_percentage` + `actor_name` must land first
2. **`metric=audit_log`** — no new data model dependencies, unblocks the dashboard feed
3. **`metric=top_teams`** — requires changelog → flags join; test JSONB query performance
4. **`metric=launched_wins`** — depends on `rollout_percentage`
5. **`metric=velocity`** — depends on `rollout_percentage`, slightly more complex subquery
