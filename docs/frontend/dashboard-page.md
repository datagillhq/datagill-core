# Frontend: Dashboard Page — Design Context

## Overview

The dashboard is the homepage post-sign-in. It gives managers and executives visibility into production impact, launched features, top teams, deployment velocity, and a persistent activity feed — all filterable by date range, team, and person.

All data is served through `GET /v1/analytics?metric=...` — see `docs/backend/analytics-api.md` for the full backend design including the plugin architecture.

---

## Page Layout

```
┌─────────────────────────────────────────────────────────────┐
│ Nav: Filter by Team ▾  Filter by Person ▾       [Date Range]│
├─────────────────────────────────────────────────────────────┤
│ Launched Wins (table)                  [View All →]         │
├──────────────────────┬──────────────────┬───────────────────┤
│ Top Teams (grid)     │                  │ Deployment        │
│                      │                  │ Velocity          │
├──────────────────────┴──────────────────┴───────────────────┤
│ Audit Log (persistent activity feed)                        │
└─────────────────────────────────────────────────────────────┘
```

AI summary has been removed from MVP scope — it will be addressed in a separate design doc covering caching and precompute strategy.

---

## Global Filters

### Date Range Filter
Located top-right. Controls all sections simultaneously.
- Default: last 7 days
- Options: last 7 days, last 30 days, last 90 days, custom range
- When changed: all sections re-fetch in parallel with the new range

### Filter by Team
Dropdown populated from distinct `maintainer.team.name` values in the org's flags. Scopes all sections to flags owned by the selected team.

### Filter by Person
Dropdown populated from distinct actor emails in the org's changelog. Scopes all sections to changes made by the selected actor.

When both Team and Person filters are active, they AND together.

---

## Section-by-Section Data Requirements

### Launched Wins (table)
Flags that reached 100% production rollout in the timeframe. Partial rollouts shown with a different badge.

| Field | Source |
|---|---|
| Flag name (links to source provider) | `GET /v1/analytics?metric=launched_wins` → `flagName`, `sourceUrl` |
| Actor name + avatar | `actorName`, `actorEmail` (avatar: Gravatar or initials fallback) |
| Rollout % badge | `rolloutPercentage` |
| Date | `occurredAt` |

`sourceUrl` links back to the flag in the provider (e.g., LaunchDarkly). DataGill does not have a native flag detail page — users go to the source for flag-level detail.

### Top Teams (grid)
Teams ranked by change count on flags they own. 6 teams in a 2×3 grid.

| Field | Source |
|---|---|
| Team name | `GET /v1/analytics?metric=top_teams` → `teamName` |
| Change count | `changeCount` |
| Active members | `activeMembers` |

### Deployment Velocity (card)
Average time from flag creation to 100% production rollout.

| Field | Source |
|---|---|
| Average time | `GET /v1/analytics?metric=velocity` → `averageTimeToFullRolloutMinutes` |
| Sample size | `sampleSize` — show as subtext ("based on 14 flags"). Disclaimer if < 3. |

Format minutes: under 60 → "Xm", over 60 → "Xh Ym", over 1440 → "X days".

### Audit Log (persistent activity feed)
Always present at the bottom of the dashboard. Paginated. Gives the user something meaningful when headline metrics have no data for the period.

| Field | Source |
|---|---|
| Timestamp, flag name, actor, event type, environments | `GET /v1/analytics?metric=audit_log` |
| Source link per entry | `sourceUrl` — links to flag in provider |

---

## Loading Strategy

**Page Load**
1. [t=0ms] Page shell renders, skeletons for all sections. Date range defaults to last 7 days.
2. [t=0ms] All 4 requests fire in parallel:
   - GET /v1/analytics?metric=launched_wins&from=&to=
   - GET /v1/analytics?metric=top_teams&from=&to=
   - GET /v1/analytics?metric=velocity&from=&to=
   - GET /v1/analytics?metric=audit_log&from=&to=&page=1&limit=25
3. [t=~100ms] All sections resolve and fill in

**User changes date range filter**
4. [t=0ms] All 4 requests re-fire in parallel — sections show skeletons
5. [t=~100ms] Sections fill in with new data

**User paginates audit log**
6. [t=0ms] GET /v1/analytics?metric=audit_log&from=&to=&page=N&limit=25
7. [t=~80ms] Audit log updates

---

## UX Details

### Empty States
- **Launched Wins empty:** "No flags reached 100% rollout in this period."
- **Top Teams empty:** "No team activity in this period."
- **Velocity empty:** "Not enough data — no flags completed full rollout in this period."
- **Audit Log empty:** "No activity recorded in this period."

Sections fail independently — one section erroring does not block others.

### Actor Avatars
No avatar URL from the API. Use Gravatar derived from actor email, with initials fallback.

### Source Links
Launched Wins rows and Audit Log entries include `sourceUrl` linking to the flag in the provider. Shown as a small external link icon next to the flag name.

### Filter Dropdown Population
Team names and actor emails for the filter dropdowns can be derived from the analytics responses already loaded on the page, or fetched separately. Decide during implementation — either approach avoids a dedicated endpoint.

---

## What the Frontend Repo Needs to Know About the Backend

- All endpoints are versioned: `/v1/analytics?metric=...`
- Auth: JWT as `Authorization: Bearer <token>`, API gateway injects `X-Org-Id`
- Backend returns error codes, not messages: `{ "code": "..." }` — frontend maps to display strings
- `launched_wins` and `velocity` metrics require `rollout_percentage` data model change on backend — coordinate before building those components
- `actor_name` must be populated before Launched Wins actor column is meaningful
- AI summary is not available in MVP
