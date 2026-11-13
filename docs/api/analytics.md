# Analytics API

`GET /v1/analytics?metric=<name>&from=<RFC3339>&to=<RFC3339>`

One endpoint serves every metric. Each metric is a plugin in `services/search/analytics/`;
adding one needs no routing changes. Requires `Authorization: Bearer <JWT>`; the org comes
from the token's `organizationId` claim, so a caller can only read its own org.

## Query parameters

| Param | Required | Notes |
|-------|----------|-------|
| `metric` | yes | `launched_wins`, `top_teams`, `velocity`, `audit_log`, `pull_requests` |
| `from`, `to` | yes | RFC 3339 timestamps, e.g. `2026-11-01T00:00:00Z`. Inclusive |
| `team` | no | Maintainer team name. Used by `launched_wins` and `audit_log` |
| `actor` | no | Actor email. Used by `launched_wins` and `audit_log`. ANDs with `team` |
| `page` | no | Default 1. Used by `audit_log` and `pull_requests` |
| `limit` | no | Default 25, capped at 100. Used by `audit_log` and `pull_requests` |

## Response envelope

```json
{
  "orgId": "uuid",
  "from": "2026-11-01T00:00:00Z",
  "to": "2026-11-13T00:00:00Z",
  "data": { "<metric>": { ...metric response... } }
}
```

## Errors

`400` with `{"code": "..."}`: `ANALYTICS_MISSING_METRIC`, `ANALYTICS_MISSING_DATE_RANGE`,
`ANALYTICS_INVALID_DATE_FORMAT`, `ANALYTICS_INVALID_PAGE`, `ANALYTICS_INVALID_LIMIT`,
`ANALYTICS_UNKNOWN_METRIC`, `INVALID_ORG_ID`. `500` with `INTERNAL_ERROR`. `401` for a
missing or invalid token.

## Metrics

### `launched_wins`

Flags with a production change in the window that left them at a rollout above 0%. One row per
flag (its latest such change), newest first.

```json
{ "wins": [ { "flagKey": "uuid", "flagName": "Apple Pay", "actorEmail": "marcus@acme.com",
              "actorName": "Marcus Chen", "rolloutPercentage": 100,
              "occurredAt": "2026-11-12T00:00:00Z" } ] }
```

### `top_teams`

Teams ranked by the number of flag changes in the window. Team is the flag's current
maintainer team (`feature_flags.data.maintainer.team.name`); flags without one are excluded.

```json
{ "teams": [ { "teamName": "Platform Eng", "changeCount": 842 } ] }
```

### `velocity`

Org-wide average minutes from a flag's creation in the provider to its first change in the
window, for flags now at 100% rollout in some environment. Creation time is LaunchDarkly's
`creationDate`; when that is missing, or the flag was first stored before it was recorded, it
falls back to when DataGill first stored the flag. A rough signal: show a caveat when
`sampleSize` is small.

```json
{ "averageTimeToFullRolloutMinutes": 12.4, "sampleSize": 14 }
```

### `audit_log`

Flag changes in the window, newest first, paginated.

```json
{ "total": 312, "page": 1, "count": 25,
  "data": [ { "occurredAt": "2026-11-12T14:12:04Z", "flagName": "Apple Pay", "flagKey": "uuid",
              "actorEmail": "sarah@acme.com", "actorName": "Sarah Jenkins",
              "eventType": "toggled", "environments": ["production"] } ] }
```

`eventType` is one of `created`, `updated`, `toggled`, `deleted`.

### `pull_requests`

Merged GitHub PRs with `merged_at` in the window, newest first, paginated, with the flag keys
each PR added or removed.

```json
{ "total": 1, "page": 1, "count": 1,
  "data": [ {
    "key": "uuid", "title": "Add apple pay flag", "state": "merged",
    "url": "https://github.com/acme/web/pull/42", "number": 42,
    "repository": { "owner": "acme", "name": "web", "id": "123" },
    "author": { "login": "octocat", "id": 1 },
    "labels": [], "assignees": [ { "login": "octocat", "id": 1 } ],
    "createdAt": "2026-11-10T09:00:00Z", "mergedAt": "2026-11-12T10:00:00Z",
    "diff": { "sha": "abc123", "branches": { "source": "feature", "target": "main" },
              "stats": { "additions": 10, "deletions": 2, "changedFiles": 3 } },
    "enrichment": { "status": "completed", "enrichedAt": "2026-11-12T10:30:00Z",
                    "featureFlags": { "added": ["apple-pay"], "removed": [] } }
  } ] }
```

`enrichment.status` is `pending`, `in_progress`, `completed` or `failed`.

## Privacy note

`launched_wins` and `audit_log` return actor emails and names to any member of the org. The
planned privacy model is described in the README roadmap.
