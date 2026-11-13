# Backend API Implementation Plan

This plan covers all backend changes required to support the frontend design reviewed in
`/tmp/datagill-design-review.md`. It lives alongside the original `IMPLEMENTATION_PLAN.md`,
which covers the ETL worker and infrastructure phases — this plan is focused solely on the
API surface exposed to the frontend (and third-party consumers).

---

## Engineering Conventions

These apply to every PR in this plan — no exceptions.

**PR size:** 100–150 lines of code maximum. Each step below is sized to fit roughly one PR.
If a step grows beyond that, split it by concern (e.g., repository methods in one PR,
handlers in the next) rather than shipping an oversized PR. Reviewability is non-negotiable.

**Testing before opening a PR:**
- All new code paths must have tests written as part of the same PR — not as follow-ups.
- Run `make test` locally and confirm it passes before opening the PR.
- Handler tests use `httptest` with mocked repository interfaces.
- Repository tests are integration tests using testcontainers (real Postgres) — same pattern
  as existing `postgres_*_test.go` files. No mocking the DB.

**Docker:**
- `docker compose up -d` must succeed after every PR that touches service code or config.
- If a PR adds a new env var, it must also update `.env.example` in the same PR.
- If a PR changes the search service behavior, manually smoke-test the running container
  before opening the PR (e.g., `curl localhost:8082/healthz`, then hit the changed route).

---

## What We're Building

| Area | Change |
|---|---|
| Data models | Add `rollout_percentage` to `FlagEnvironment`; add `actor_name` to `ChangelogActor` |
| Auth | Replace `X-Org-Id` header middleware with JWT validation (BetterAuth-issued tokens) |
| Integrations API | 5 new endpoints — create, list, update, delete integrations; list supported providers |
| Analytics API | `GET /v1/analytics?metric=...` with plugin architecture; 4 initial metrics |
| Deprecations | Remove `/search`, `/flags`, `/flags/{flag_key}`, `/flags/{flag_key}/history`, `/timeline` |
| URL versioning | All new routes under `/v1/` prefix |

---

## Execution Order

```
Step A: Data Model Changes       ← no external deps, unblocks Steps C and D
    │
    ├── Step B: JWT Middleware    ← unblocks all authenticated routes
    │
    ├── Step C: Integrations API ← after A (needs updated models) + B (auth)
    │
    └── Step D: Analytics API    ← after A (needs rollout_percentage + actor_name) + B (auth)
```

Steps C and D can be worked in parallel after A and B are done. Within each step, tasks are
sequential unless noted otherwise.

---

## Step A: Data Model Changes

**Goal:** Add the two shared prerequisites that `launched_wins`, `velocity`, and actor display
all depend on. These are JSONB fields — no DB schema migration needed, only model + normalization
changes.

### A.1 — `rollout_percentage` on `FlagEnvironment`

File: `models/canonical.go`

```go
// Before
type FlagEnvironment struct {
    Enabled bool `json:"enabled"`
}

// After
type FlagEnvironment struct {
    Enabled           bool     `json:"enabled"`
    RolloutPercentage *float64 `json:"rollout_percentage,omitempty"`
}
```

`*float64` (pointer) because `null` and `0` mean different things: null = not a rollout-style
flag; 0 = rolled out to 0%.

File: `activities/launchdarkly/normalize_flag.go`
- Populate `RolloutPercentage` from the LD flag detail response
- LD stores this per-environment under `environments[env].fallthrough.rollout.variations`
  or as a simpler on/off toggle. Capture the percentage if present; leave nil otherwise.

### A.2 — `actor_name` on `ChangelogActor`

File: `models/canonical.go`

```go
// Before
type ChangelogActor struct {
    Email string `json:"email,omitempty"`
}

// After
type ChangelogActor struct {
    Email string `json:"email,omitempty"`
    Name  string `json:"name,omitempty"`
}
```

File: `activities/launchdarkly/normalize_changelog.go` (or wherever `extractActor` lives)
- Populate `Name` from the LD audit entry's actor display name field

### A.3 — Tests

Update existing normalization unit tests (table-driven) to cover:
- Flag with rollout percentage present → `RolloutPercentage` populated
- Flag without rollout percentage → `RolloutPercentage` nil
- Changelog entry with actor name → `Name` populated
- Changelog entry without actor name → `Name` empty string (not nil — it's a value type)

---

## Step B: JWT Middleware

**Goal:** Replace the `X-Org-Id` header trust model with BetterAuth-issued JWT validation.
The new middleware validates the JWT signature, then extracts `organizationId` and `userId`
claims for downstream handlers.

### B.1 — Action item first

Before writing any code: verify that BetterAuth's JWT plugin includes `activeOrganizationId`
(or `organizationId`) as a claim and document the exact claim key name. The rest of this step
assumes the claim key is confirmed.

### B.2 — Dependencies

Add to `services/search/go.mod`:
- `github.com/golang-jwt/jwt/v5` — JWT parsing and validation
- `github.com/MicahParks/keyfunc/v3` — JWKS fetching with automatic key rotation

### B.3 — JWKS caching

File: `services/search/jwks.go`

On `app` startup (in `main.go`), fetch BetterAuth's JWKS endpoint
(`BETTERAUTH_URL + "/.well-known/jwks.json"`) and cache the key function. The `keyfunc`
library handles periodic refresh — configure a refresh interval of 5 minutes so key
rotation propagates without a restart.

```go
type app struct {
    // existing fields...
    jwks *keyfunc.JWKS
}
```

Surface startup errors clearly — if the JWKS endpoint is unreachable on boot, the service
should not start (fast fail, not silent degradation).

### B.4 — JWT middleware

File: `services/search/middleware.go`

Replace `orgIDMiddleware` with `jwtMiddleware`:

```go
func (a *app) jwtMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 1. Extract Bearer token from Authorization header
        // 2. Parse and validate signature using a.jwks
        // 3. Validate standard claims: exp, iss
        // 4. Extract organizationId and userId claims
        // 5. Reject with 401 if any step fails
        // 6. Inject claims into context + zerolog logger
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

Context keys: define `orgIDKey` and `userIDKey` typed constants (same pattern as existing
`orgIDKey` for `X-Org-Id`).

File: `services/search/router.go`
- Remove `r.Use(orgIDMiddleware)` from the authenticated group
- Add `r.Use(a.jwtMiddleware)` in its place
- The `app` receiver is now needed on the middleware, so switch to a method

### B.5 — Tests

File: `services/search/middleware_test.go`

Table-driven tests using `httptest`:
- Valid JWT with all claims → 200, claims in context
- Missing Authorization header → 401
- Malformed token → 401
- Expired token → 401
- Valid signature, missing `organizationId` claim → 401

Generate a test RSA key pair in the test file — no external dependencies.

---

## Step C: Integrations API

**Goal:** Full CRUD for integrations, plus a provider catalog endpoint. The org record in
DataGill's DB is created lazily on first integration (no explicit org endpoint).

### C.1 — `IntegrationConfig` cleanup

File: `models/integration.go`

Remove `ProjectKey`, `PollIntervalMinutes`, and `BackfillDays` from `IntegrationConfig`.
DataGill owns poll interval and backfill internally — they are not client-configurable.
Integrations are project-agnostic; project context lives at the feature flag level.

```go
// After
type IntegrationConfig struct {
    EnvironmentKey string `json:"environment_key"`
}
```

**Important:** The ETL worker reads `IntegrationConfig` to know which LD environment to sync.
After removing `ProjectKey`, verify the worker still has everything it needs. `EnvironmentKey`
remains — the LD project key is derivable from LD's own API or stored at the flag level, not
the integration level.

### C.2 — Repository methods

File: `repository/postgres_integrations.go`

New or updated methods needed:

| Method | Notes |
|---|---|
| `CreateIntegration(ctx, Integration) (Integration, error)` | Inserts new row; returns created integration |
| `GetIntegration(ctx, orgID, key uuid.UUID) (Integration, error)` | Single row by key, scoped to org |
| `ListIntegrations(ctx, orgID uuid.UUID) ([]Integration, error)` | All non-deleted for org |
| `UpdateIntegration(ctx, orgID, key uuid.UUID, patch IntegrationPatch) (Integration, error)` | Partial update — only fields present in patch |
| `SoftDeleteIntegration(ctx, orgID, key uuid.UUID) (Integration, error)` | Sets `state = deleted`, returns updated row |
| `OrgExists(ctx, orgID uuid.UUID) (bool, error)` | Used to decide whether to create org record lazily |
| `CreateOrg(ctx, orgID uuid.UUID) error` | Inserts minimal org row on first integration |

`IntegrationPatch` is a struct with pointer fields — nil means "do not change":
```go
type IntegrationPatch struct {
    APIKey *string
    State  *IntegrationState
}
```

All methods must be covered by integration tests (`postgres_integrations_test.go`) using
testcontainers — same pattern as existing integration tests.

### C.3 — Provider validation

File: `services/search/integration_validator.go` (or inject via interface into the handler)

When `POST /v1/integrations` or `PUT /v1/integrations/{key}` receives a new API key:
- Make a lightweight authenticated request to the provider's API (e.g., GET to LD's
  `/api/v2/account` or similar canary endpoint) with a 5s timeout
- On 200: key is valid, proceed
- On 401/403: return `INTEGRATION_INVALID_API_KEY`
- On timeout / 5xx: return `INTEGRATION_PROVIDER_UNREACHABLE`

This is injected as an interface so handlers can be tested without real provider calls:
```go
type IntegrationValidator interface {
    Validate(ctx context.Context, provider Source, apiKey string) error
}
```

### C.4 — Handlers

File: `services/search/handlers_integrations.go`

Five handlers, all on `app` receiver:

| Handler | Route | Notes |
|---|---|---|
| `providersHandler` | `GET /v1/integrations/providers` | Returns `{ "providers": ["launchdarkly"] }` — hardcoded list of supported providers |
| `createIntegrationHandler` | `POST /v1/integrations` | Validates key, creates org if needed, creates integration; 201 on success |
| `listIntegrationsHandler` | `GET /v1/integrations` | Lists all non-deleted integrations for org |
| `updateIntegrationHandler` | `PUT /v1/integrations/{key}` | Partial update — if `apiKey` absent, keep existing key; if present, re-validate |
| `deleteIntegrationHandler` | `DELETE /v1/integrations/{key}` | Soft delete; returns `{ "key": "...", "state": "deleted" }` |

API key is **never returned** in any response. Strip it before serializing.

Error code mapping (backend returns the code, frontend owns display strings):

| Scenario | HTTP status | Code |
|---|---|---|
| Invalid API key | 400 | `INTEGRATION_INVALID_API_KEY` |
| Provider unreachable | 400 | `INTEGRATION_PROVIDER_UNREACHABLE` |
| Duplicate provider for org | 409 | `INTEGRATION_DUPLICATE_PROVIDER` |
| Integration not found | 404 | `INTEGRATION_NOT_FOUND` |

All error responses: `{ "code": "..." }` — no message field.

### C.5 — Router update

File: `services/search/router.go`

Add under the authenticated `/v1/` group:
```
GET    /v1/integrations/providers
POST   /v1/integrations
GET    /v1/integrations
PUT    /v1/integrations/{key}
DELETE /v1/integrations/{key}
```

### C.6 — Tests

File: `services/search/handlers_integrations_test.go`

Handler-level tests using `httptest`. Mock the repository and validator interfaces.
Cover: happy paths for all 5 endpoints, each error code case, missing/invalid JWT (401),
wrong org scoping (404 not 403 — never leak existence across orgs).

---

## Step D: Analytics API

**Goal:** A single `GET /v1/analytics?metric=...` endpoint backed by a plugin architecture.
Each metric is a self-contained registered plugin. Adding a new metric = writing one new
plugin file.

### D.1 — Plugin interface

File: `services/search/analytics/plugin.go`

```go
type Query struct {
    OrgID  uuid.UUID
    From   time.Time
    To     time.Time
    Team   string // empty = no filter
    Actor  string // empty = no filter
    Page   int    // for paginated metrics
    Limit  int    // for paginated metrics
}

type Plugin interface {
    Metric() string
    Execute(ctx context.Context, q Query) (any, error)
}
```

File: `services/search/analytics/registry.go`

```go
type Registry struct {
    plugins map[string]Plugin
}

func (r *Registry) Register(p Plugin)
func (r *Registry) Get(metric string) (Plugin, bool)
```

### D.2 — Repository queries

File: `repository/postgres_analytics.go`

One method per metric query. All scoped to org ID and date range.

| Method | Notes |
|---|---|
| `QueryLaunchedWins(ctx, orgID, from, to, team, actor)` | Flags where `rollout_percentage = 100` entered production in window. Requires `rollout_percentage` (Step A.1). |
| `QueryTopTeams(ctx, orgID, from, to)` | Count changelog entries grouped by `feature_flags.data->maintainer->team->name` |
| `QueryVelocity(ctx, orgID, from, to)` | Average `time_to_100 = timestamp_of_100pct_event − feature_flags.created_at` for flags that hit 100% in window |
| `QueryAuditLog(ctx, orgID, from, to, team, actor, page, limit)` | Paginated raw changelog; return total count + page of entries |

`team` and `actor` filters AND together when both present.

> **Note on JSONB team filter:** Querying `feature_flags.data->'maintainer'->'team'->>'name'`
> will work at MVP scale. If query performance becomes an issue at larger data volumes, add a
> generated stored column + GIN index — but do not add it preemptively.

### D.3 — Metric plugins

One file per metric under `services/search/analytics/`:

**`launched_wins.go`**
- Calls `QueryLaunchedWins`
- Returns array of `{ flagKey, flagName, sourceUrl, actorEmail, actorName, rolloutPercentage, occurredAt }`

**`top_teams.go`**
- Calls `QueryTopTeams`
- Returns array of `{ teamName, changeCount, activeMembers }`

**`velocity.go`**
- Calls `QueryVelocity`
- Returns `{ averageTimeToFullRolloutMinutes, sampleSize }`
- When `sampleSize < 3`, the frontend shows a disclaimer — the backend always returns `sampleSize`

**`audit_log.go`**
- Calls `QueryAuditLog`
- Returns paginated response: `{ total, page, count, data: [...] }`
- `data` entries: `{ occurredAt, flagName, flagKey, sourceUrl, actorEmail, actorName, eventType, environments }`

### D.4 — Handler

File: `services/search/handlers_analytics.go`

Single handler `analyticsHandler` on `app` receiver:
1. Parse `metric`, `from`, `to`, `team`, `actor`, `page`, `limit` query params
2. `from`/`to` required; default `page=1`, `limit=25`
3. Look up metric in registry — 400 with `{ "code": "ANALYTICS_UNKNOWN_METRIC" }` if not found
4. Call `plugin.Execute(ctx, query)`
5. Wrap result: `{ "orgId": "...", "metric": "...", "from": "...", "to": "...", ...data }`

### D.5 — Router update

File: `services/search/router.go`

Add under the authenticated `/v1/` group:
```
GET /v1/analytics
```

**Remove** the following routes (replaced by `/v1/analytics`):
```
GET /search
GET /flags
GET /flags/{flag_key}
GET /flags/{flag_key}/history
GET /timeline
```

Remove the corresponding handlers from `handlers.go` only after `GET /v1/analytics` is
complete and tested — do not delete working code before the replacement exists.

### D.6 — Tests

File: `services/search/handlers_analytics_test.go`

Handler-level tests (mock repository): one test per metric covering happy path + empty result.
Also test: unknown metric → 400, missing `from`/`to` → 400, invalid date format → 400.

File: `repository/postgres_analytics_test.go`

Integration tests (testcontainers) for each query method. Seed known data, assert known
output. Cover edge cases: empty window, `sampleSize < 3`, both `team` and `actor` filters
active simultaneously.

---

## What This Does Not Cover

- **Frontend (datagill-ui):** Separate repo, separate plan. The contracts above are the
  interface boundary — frontend can be built against these once the API is stable (or mocked
  locally in the interim).
- **ETL Worker (Phase 2A):** Covered in `docs/IMPLEMENTATION_PLAN.md`. The data model
  changes in Step A above (particularly `rollout_percentage` population) extend the ETL
  worker's normalization, but the worker scaffolding itself is tracked separately.
- **BetterAuth setup:** Auth infrastructure (JWT plugin config, JWKS endpoint) is a BetterAuth
  configuration task, not a DataGill backend task. Confirm `organizationId` claim shape before
  starting Step B.
- **`last_synced_at` / `last_error` on Integration:** Deferred from MVP per design review.
