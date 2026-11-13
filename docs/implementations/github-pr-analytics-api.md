# GitHub PR Analytics API Plan

> Status: Implemented as `GET /v1/analytics?metric=pull_requests`.
> Companion to [github-integration.md](./github-integration.md) and [github-pr-enrichment.md](./github-pr-enrichment.md).

## Summary

Expose ingested GitHub pull request data through the existing analytics API with a new metric:

```http
GET /v1/analytics?metric=pull_requests&from=<RFC3339>&to=<RFC3339>&page=1&limit=25
```

This keeps the first front-facing GitHub data surface aligned with the current `/v1/analytics` plugin architecture. The goal is to prove that GitHub data is being ingested and is visible to the frontend without creating a separate pull-request route family yet.

## API Shape

The endpoint uses the existing analytics response envelope:

```json
{
  "orgId": "org-uuid",
  "from": "2026-05-01T00:00:00Z",
  "to": "2026-05-08T00:00:00Z",
  "data": {
    "pull_requests": {
      "total": 12,
      "page": 1,
      "count": 12,
      "data": [
        {
          "key": "pr-uuid",
          "title": "feat: add checkout flag",
          "state": "merged",
          "url": "https://github.com/acme/web/pull/42",
          "number": 42,
          "repository": {
            "owner": "acme",
            "name": "web",
            "id": "123456"
          },
          "author": {
            "login": "ariel",
            "id": 12345
          },
          "labels": ["feature"],
          "assignees": [
            {
              "login": "jordan",
              "id": 67890
            }
          ],
          "createdAt": "2026-05-01T10:00:00Z",
          "mergedAt": "2026-05-02T10:00:00Z",
          "diff": {
            "sha": "abc123",
            "branches": {
              "source": "feat/checkout",
              "target": "main"
            },
            "stats": {
              "additions": 120,
              "deletions": 45,
              "changedFiles": 8
            }
          },
          "enrichment": {
            "status": "completed",
            "enrichedAt": "2026-05-02T10:10:00Z",
            "featureFlags": {
              "added": ["flag-uuid"],
              "removed": []
            }
          }
        }
      ]
    }
  }
}
```

## Implementation Changes

- Add a `pull_requests` analytics plugin under `services/search/analytics`.
- Register the plugin in `analytics.RegisterAll`.
- Add a repository query scoped by `org_id`, filtering PRs by source `merged_at` in `[from, to]`, ordered newest first.
- Reuse the existing analytics pagination parameters: `page` defaults to `1`, `limit` defaults to `25`, and `limit` is capped at `analytics.MaxLimit`.
- Join `pull_requests` to `pull_request_enrichments` with a left join so the response shows enrichment status and any discovered feature flag references.
- Treat missing enrichment JSON as an empty result with the row's current enrichment status.
- Ignore `team` and `actor` query params for this metric; those are flag-analytics filters.
- Do not add a new DB index for v1. Add an expression index on `pull_requests.data->'source'->>'merged_at'` later only if this list becomes slow.

## Test Plan

- Handler test: `GET /v1/analytics?metric=pull_requests...` returns the existing envelope with `data.pull_requests`.
- Plugin unit test: maps repository rows into the public response, including nil enrichment data as empty feature flag arrays.
- Repository integration test: verifies org scoping, date filtering by `merged_at`, descending order, pagination, total count, and enrichment join output.
- Regression check: existing invalid auth, invalid date, invalid page, and invalid limit analytics behavior remains unchanged.
- Run:

```bash
go test ./services/search/... ./repository/...
```

## Assumptions

- This is a demo/read visibility API, not a full GitHub search surface.
- Date filtering is based on source `merged_at`; if `merged_at` is absent, the PR should not appear in a merged-PR list.
- `pull_requests.state` remains the operational state, with `merged` as the expected v1 value.
- A dedicated `GET /v1/pull-requests` route can be added later if GitHub PR browsing grows beyond the analytics dashboard.
