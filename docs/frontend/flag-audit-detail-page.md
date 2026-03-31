# ⚠️ Removed from MVP

The Feature Flag Audit Detail page has been removed from scope. DataGill is a business intelligence tool — for flag-level detail, users are linked directly to their source provider (LaunchDarkly, etc.) via `sourceUrl` in analytics responses.

Flag-level analytics (`GET /v1/flags/{key}/analytics?metric=...`) may be revisited in a future phase and will share the same metric namespace as `GET /v1/analytics`.

The content below is preserved for reference only.

---

# Frontend: Flag Audit Detail Page — Design Context

## Overview

This document captures the UX design research and loading strategy for the **Feature Flag Audit History Detail** page. It covers what each section of the page needs from the API, the loading strategy to meet the <400ms perceived load target, and open questions for the frontend implementation plan.

The backend counterpart is in `docs/backend/flag-audit-detail-api.md`. Read both before creating an implementation plan.

---

## Page Layout (Mock Summary)

The page is reached by clicking a flag from the Feature Flags list. It has five main sections:

```
┌─────────────────────────────────────────────────────────────┐
│ Header: flag name, status badge, last modified by           │
├───────────────┬───────────────────┬─────────────────────────┤
│ Lifecycle     │ Contributor       │ 30-Day Activity         │
│ Summary       │ Insights          │ Calendar                │
├───────────────┴───────────────────┴─────────────────────────┤
│ Change Frequency Graph                                       │
├─────────────────────────────────────────────────────────────┤
│ Detailed Audit Log (paginated table)                         │
└─────────────────────────────────────────────────────────────┘
```

The header and three summary cards are **above the fold**. The change frequency graph and audit log are **below the fold**.

---

## Section-by-Section Data Requirements

### Header
- Flag name and status badge (`ACTIVE` / `OFF` / etc.)
- "Last Modified X ago by [Name]"

| Field | API Source | Notes |
|-------|-----------|-------|
| Flag name | `GET /flags/{key}` → `name` | Also available from router state (see loading strategy) |
| Status badge | `GET /flags/{key}` → `state` | Also available from router state |
| Last modified time | `GET /flags/{key}` → `updatedAt` | |
| Last modified by (name) | `GET /flags/{key}/history?limit=1` → first entry `actorName` | Requires `actor_name` data model change on backend |

### Lifecycle Summary Card
| Field | API Source | Notes |
|-------|-----------|-------|
| Creation date | `GET /flags/{key}/stats` → `createdAt` | |
| Total mutations | `GET /flags/{key}/stats` → `totalMutations` | |
| Production rollout % | `GET /flags/{key}/stats` → `productionRollout` | Requires `rollout_percentage` data model change on backend |

### Contributor Insights Card
| Field | API Source | Notes |
|-------|-----------|-------|
| Actor name | `GET /flags/{key}/contributors?days=30` → `actorName` | |
| Actor avatar | Not available from API | Use email-based avatar service (e.g., Gravatar) or initials fallback |
| Change count | `GET /flags/{key}/contributors?days=30` → `changes` | |
| Window | Fixed 30-day. No timeframe toggle. | |

### 30-Day Activity Calendar
GitHub contributions-style heatmap. Each cell = one day, color intensity = change count.

| Field | API Source | Notes |
|-------|-----------|-------|
| Daily counts | `GET /flags/{key}/activity?days=30` → `activity[]` | Backend fills in zero-count days |

### Change Frequency Graph
Line chart of daily mutations over time. Uses the **same data as the activity calendar** — no separate endpoint needed.

| Field | API Source | Notes |
|-------|-----------|-------|
| Daily counts | Reuse `activity` data already fetched | Frontend renders the same dataset as a line chart |
| Release cycle annotations | Out of scope | Mock shows `v2.4 Release`, `v2.5 Release` labels — no data source for this exists |

### Detailed Audit Log
Paginated table with: timestamp, actor name + avatar, action label, environment, diff preview.

| Field | API Source | Notes |
|-------|-----------|-------|
| Timestamp | `GET /flags/{key}/history` → `occurredAt` | |
| Actor name + avatar | `GET /flags/{key}/history` → `actorName` | Avatar: Gravatar or initials fallback |
| Action label | `GET /flags/{key}/history` → `actionLabel` | e.g. "Target Rule Updated", "Killswitch Engaged" — requires `action_label` backend change |
| Environment | `GET /flags/{key}/history` → `environments[0]` | |
| Diff preview | `GET /flags/{key}/history` → `diff[]` | Requires `diff` data model change on backend |
| Total count | `GET /flags/{key}/history` → `total` | Requires adding `total` to history response |
| Pagination | `?page=&limit=25` | Page-based for now; cursor-based is a future improvement |

---

## Loading Strategy

### Performance Target
The user should see meaningful above-the-fold content within **400ms**. Nothing should block rendering.

### Navigation State (Free — No Network Request)
When the user navigates from the Feature Flags list, the router passes flag `name` and `state` (status badge) through navigation state. These render **instantly** — no waiting for any API call.

Show skeleton loaders for all other sections immediately on mount.

### Wave 1 — Fire in Parallel on Mount
These are all above-the-fold and independent of each other:

```
GET /flags/{key}                     → fills in last modified time + actor
GET /flags/{key}/stats               → fills in lifecycle summary card
GET /flags/{key}/contributors?days=30 → fills in contributor insights card
GET /flags/{key}/activity?days=30    → fills in calendar + frequency graph data
```

Expected resolution order (approximate):
- `/flags/{key}` → ~80ms
- `/stats` → ~120ms
- `/contributors` → ~150ms
- `/activity` → ~200ms

### Wave 2 — Deferred (IntersectionObserver)
These are below the fold. Fire when the user scrolls near them:

```
GET /flags/{key}/history?page=1&limit=25   → audit log table
```

The change frequency graph does **not** need a new request — it renders from the `/activity` data already fetched in Wave 1.

### Pagination
When the user clicks Next/Previous in the audit log:
```
GET /flags/{key}/history?page=N&limit=25
```

### Sequence Diagram

```
Browser                                     API
   |                                          |
   | [navigate from flags list]               |
   | router state: name, status → render instantly (no network)
   | skeleton shown for all other sections    |
   |                                          |
   |-- GET /flags/{key} --------------------->|
   |-- GET /flags/{key}/stats --------------->|  all fire
   |-- GET /flags/{key}/contributors?days=30->|  in parallel
   |-- GET /flags/{key}/activity?days=30 ---->|
   |                                          |
   |<-- /flags/{key} (~80ms) -------------- |  header completes
   |<-- /stats (~120ms) ------------------- |  lifecycle card fills
   |<-- /contributors (~150ms) ------------ |  contributors card fills
   |<-- /activity (~200ms) ---------------- |  calendar + graph fill
   |                                          |
   |  [user scrolls down]                     |
   |-- GET /flags/{key}/history?page=1 ------>|
   |<-- history (~80ms) -------------------- |  audit log renders
   |                                          |
   |  [user clicks Next]                      |
   |-- GET /flags/{key}/history?page=2 ------>|
   |<-- history (~80ms) -------------------- |  audit log updates
```

---

## UX Details

### Skeletons
All sections should show skeleton placeholders on mount before data arrives. Do not show empty states until data has had a chance to load.

### Actor Avatars
The API returns actor email and name. There is no avatar URL from the backend. Options:
- **Gravatar**: derive URL from MD5 hash of email
- **Initials fallback**: render initials in a colored circle if no Gravatar exists

### Error States
Each section should fail independently. If `/contributors` fails, the contributors card shows an error state — it should not block the rest of the page.

### Export Audit Button
Visible in the mock header. No export endpoint exists yet on the backend. Treat as out of scope for now; disable or hide the button until the backend implements it.

### Manage Flag Button
This should link out to the flag in the source provider (e.g., LaunchDarkly). The flag's source URL can be derived from the provider key and project key available in `FlagDetail.source`.

---

## What the Frontend Repo Needs to Know About the Backend

- **Base URL**: configured per environment
- **Auth**: send JWT as `Authorization: Bearer <token>` — the API gateway validates it and injects `X-Org-Id`; the frontend does not need to send `X-Org-Id` directly
- **All responses** follow the shape `{ orgId, count, <resource> }` or `{ orgId, flagKey, <data> }` for flag-scoped endpoints
- **Errors** return `{ error: "human-readable message" }` with appropriate HTTP status codes (400, 404, 500)
- New endpoints (`/stats`, `/contributors`, `/activity`) do not exist yet — coordinate with backend on rollout before building the frontend components that depend on them
- `actorName`, `actionLabel`, and `diff` fields in the history response also do not exist yet — the backend data model changes must land first
