# Frontend: Settings / Integrations Page — Design Context

## Overview

The `/settings/integrations` page lets users manage their connected integrations — viewing status, re-configuring, pausing, and disconnecting. New integrations can be connected here using the same modal as onboarding.

The backend counterpart is `docs/backend/integration-management-api.md`.
The shared integration modal is documented in `docs/frontend/integration-connection-modal.md`.

---

## Page Layout

```
┌─────────────────────────────────────────────────────────┐
│ Manage Integrations                  [Search] [Filters] │
│ All | Live | Filters                                    │
├─────────────────────────────────────────────────────────┤
│ ┌──────────┐ ┌──────────┐ ┌──────────┐                 │
│ │ Connected│ │ Connected│ │ Connected│                  │
│ │ LaunchDk │ │Salesforce│ │  Slack   │                  │
│ │ [toggle] │ │ [toggle] │ │ [toggle] │                  │
│ │[Configure│ │[Configure│ │[Configure│                  │
│ └──────────┘ └──────────┘ └──────────┘                 │
│ ┌──────────┐ ┌──────────┐ ┌──────────┐                 │
│ │  NEW     │ │RECOMMENDED│ │COMING    │                 │
│ │ Datadog  │ │  GitHub  │ │  SOON    │                  │
│ │[Configure│ │[Configure│ │ Sentry   │                  │
│ └──────────┘ └──────────┘ └──────────┘                 │
│                                                         │
│               [Discover More]                           │
└─────────────────────────────────────────────────────────┘
```

---

## Provider Catalog

The frontend holds the full catalog of providers with metadata (name, logo, description, tags). On page load, `GET /v1/integrations/providers` is fetched to know which providers DataGill currently supports.

- Provider ID in the backend response → show Connect button (implemented)
- Provider ID not in the backend response → show "Coming Soon" badge, Connect disabled

This means new providers can be enabled by a backend deploy alone, with no frontend change required.

---

## Integration Card States

Each card's visual state is derived from the `GET /v1/integrations` response crossed with the provider catalog:

| State | Visual | Condition |
|---|---|---|
| Connected, active | Toggle on, Configure button | Present in GET /v1/integrations, `state: active` |
| Connected, paused | Toggle off, Configure button | Present in GET /v1/integrations, `state: paused` |
| Not connected (supported) | Connect button, no toggle | Not in GET /v1/integrations, but in providers list |
| Coming soon | Grayed out, disabled | Not in providers list from backend |

Error state (Stale Error badge) is **deferred** — not in MVP scope.

---

## Data Requirements

| Field | Source |
|---|---|
| Provider name, logo, description, tags | Static frontend catalog |
| Supported provider IDs | `GET /v1/integrations/providers` |
| Connected integrations | `GET /v1/integrations` |
| Connection state (active/paused) | `integration.state` |

---

## Actions

### Connect (not-yet-connected provider)
Opens the integration connection modal in `mode: 'create'`.

```ts
onSuccess={(integration) => addToIntegrationList(integration)}
```

### Configure (connected provider)
Opens the integration connection modal in `mode: 'edit'`, with the API key field masked.

```ts
onSuccess={(integration) => updateIntegrationInList(integration)}
```

### Toggle (pause / resume)
Calls `PUT /v1/integrations/{key}` with `{ "state": "paused" }` or `{ "state": "active" }`.
- Optimistic update: flip the toggle immediately, revert on error
- No modal needed

### Disconnect
Accessible from the Configure modal or a "..." overflow menu on the card.
Calls `DELETE /v1/integrations/{key}`.
- Show confirmation dialog before disconnecting: "Disconnecting will stop syncing data from [Provider]. Historical data is retained."
- On confirm: remove card from connected list, provider moves back to "not connected" state

---

## Loading Strategy

**Page Load**
1. [t=0ms] Skeleton cards shown
2. [t=0ms] Two requests fire in parallel:
   - GET /v1/integrations
   - GET /v1/integrations/providers
3. [t=~80ms] Both resolve — browser merges responses with static catalog
   - Connected integrations show state
   - Remaining catalog providers show as "not connected" or "Coming Soon"

**On action completion** (toggle, configure, disconnect): update local list state directly from the API response — no full page re-fetch.

---

## Filters

- **All**: show all connected integrations + full catalog
- **Live**: show only `state: active` connected integrations
- **Filters**: filter by category tag (DEVOPS, CRM, etc.) — operates on static catalog metadata, no API call

---

## What This Page Shares With Onboarding

- **Integration connection modal** — identical component, `docs/frontend/integration-connection-modal.md`
- **Provider catalog** — same static data source, same `GET /v1/integrations/providers` call
- **Card visual design** — same card component, different state variants
