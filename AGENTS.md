# AGENTS.md — DataGill Platform: Source of Truth

> This document is the **single source of truth** for architecture decisions, product requirements, and open questions.
> Every agent and every PR must trace back to this document.
> Do not implement something that contradicts this document without updating it first.
> When a section says **TBD**, that means the decision has NOT been made — do not invent one.

---

## 0. North Star

DataGill is a **business intelligence platform for engineering teams**. It connects to the tools engineers use to ship software, normalizes the data into a unified model, and surfaces it through a web UI so teams can answer hard questions about their work.

| Data Source | Questions Unlocked |
|-------------|-------------------|
| Feature flags (LaunchDarkly, Unleash, Flagsmith) | What changed before the incident? How long to ship a flag? |
| Code (GitHub) | Which PR shipped this flag? Time from commit → flag on in prod? |
| Incidents (PagerDuty) | Was there an incident open when this flag was rolled back? |
| Business metrics (Datadog) | Did this flag change correlate with a latency spike? |
| Data warehouse (Snowflake) | What did conversion rate look like when this feature launched? |

**MVP = feature flag providers only.** Architecture must accommodate future data source categories without rearchitecting the pipeline.

---

## 1. Product Requirements

"All 3" = LaunchDarkly, Unleash, Flagsmith. "LD" = LaunchDarkly only.

### MVP — Lights On

| Feature | Source | Notes |
|---------|--------|-------|
| Activity feed (who changed what, when) | All 3 | LD titleVerb is cleanest canonical description |
| Flag status (off / on / partial) | All 3 | LD direct; Unleash/Flagsmith need inference |
| Trend line (changes over time) | All 3 | Aggregate by day |
| Actor attribution (last modified by) | All 3 | Normalize to actor_email |
| Search by flag name / time range | All 3 | LD supports full-text; others: time range only |

### P0 — Root Cause Analysis

| Feature | Source | Notes |
|---------|--------|-------|
| "What changed in the last X hours?" scoped to prod | All 3 | Filter by environment + time window |
| Automated vs. human change detection | LD | token.serviceToken field distinguishes API vs. human |
| Rollback detection | All 3 | Flag toggled back within short window |
| Per-flag timeline view | All 3 | Paginated history per flag, newest first |

### P1 — Developer Velocity

| Feature | Source | Notes |
|---------|--------|-------|
| Time from flag creation → on in prod | All 3 | Requires first_seen_at per flag |
| Stale flag list (created, never launched) | All 3 | No "enabled" event past X days |
| Dead flag list (launched 60+ days, no changes) | All 3 | Compare last_event_at |
| Flag cleanup suggestions | Derived | Combine stale + dead signals |
| Owner-level velocity | All 3 | Group by actor across flag history |

### P1 — Monthly Snapshot

| Feature | Source | Notes |
|---------|--------|-------|
| Flags launched this month | All 3 | Count enable/100% events |
| Flags cleaned up (deleted) | All 3 | Count delete events |
| Monthly digest | Derived | AI-generated narrative is a P2 enhancement |

### P2 — Intelligence

| Feature | Notes |
|---------|-------|
| Anomaly: high change frequency | Flag changed >N times in 24h |
| Natural language search | LLM over stored embeddings — do not rely on source q param alone |
| Risk score | Blast radius × recent change rate |
| AI narrative summaries per flag | LLM over flag event history |

---

## 2. Tech Stack

### Decided

| Layer | Choice | Rationale |
|-------|--------|-----------|
| Frontend | Next.js (App Router) + shadcn/ui | Best-in-class SSR; accessible components we own |
| Backend | Go + Chi | Goroutines fit ETL concurrency; Chi is lightweight and stdlib-compatible |
| Database | PostgreSQL 16 + pgvector | ACID, JSONB, vector search. Two instances: one for Temporal state, one for app data |
| Auth | Clerk | JWT issuance + org management; Go validates via JWKS |
| Embeddings | OpenAI text-embedding-3-small / Ollama nomic-embed-text | Post-MVP. Swappable via EmbedProvider interface |
| Orchestration | Self-hosted Temporal | Replaces queues. Durable state, retry, scheduling in one primitive |
| Migrations | golang-migrate | Run at startup or as a separate job |
| Metrics | Prometheus + Grafana | Scrape endpoint at /metrics on all services |
| Secrets | AWS Secrets Manager (cloud) / env vars + Docker secrets (self-hosted) | |
| Provider auth | API key (MVP) | Customer pastes read-only key; encrypted at rest with AES-256-GCM |
| Webhooks | None in v1 — poll only | No inbound public endpoints; customers provide API key |

### TBD

| Decision | Options | Blocking On |
|----------|---------|-------------|
| Go DB access layer | sqlc, pgx raw, GORM | LLD session |
| Hosting / compute | Railway, Fly.io, GCP Cloud Run, VPS | Cost analysis |
| Frontend data fetching | TanStack Query, SWR, RSC-only | LLD session |
| Provider auth V2 | OAuth (no manual key management) | Post-MVP |

---

## 3. Architecture

Two runtime components: a **Worker** (Temporal, handles all ETL) and a **Search Service** (HTTP query layer).

### Data Flow

| Step | Component |
|------|-----------|
| 1. Poll source APIs | LaunchDarkly / Unleash / Flagsmith |
| 2. Orchestrate workflows | Temporal (self-hosted) — Worker binary |
| 3. Store and query data | PostgreSQL app DB |
| 4. Serve queries | Search Service :8082 |
| 5. Display to users | Frontend |

### Temporal Workflow Hierarchy

Extract and transform workflows are provider-specific. Shared code lives in save activities, canonical models, and the repository layer. Adding a new provider means writing new extract/transform workflows and activities — zero changes to existing provider code.

- **FeatureFlagETLWorkflow** — one per integration, runs on schedule. Routes to provider-specific extract workflow by `integration.source`.
  - **LaunchDarklyExtractWorkflow** — provider-specific orchestrator
    - `ListAuditLogs` — sequential pagination through all audit log pages
    - `GetFlagDetail` — parallel GET per unique flag key (workflow.Go goroutines)
    - `SaveSourceRecord` — saves raw API responses to `source_records` table
    - For each audit event → fire-and-forget `LaunchDarklyChangelogTransformWorkflow` (ack start only)
  - **LaunchDarklyChangelogTransformWorkflow**
    - `NormalizeChangelog` (LD-specific)
    - `SaveChangelog` (shared)
  - For each unique flag → fire-and-forget **LaunchDarklyFeatureFlagTransformWorkflow** (ack start only)
    - `NormalizeFlagDetail` (LD-specific)
    - `SaveFeatureFlag` (shared)

**Tables written to:** `organizations` · `integrations` · `source_records` · `feature_flags` · `feature_flag_changelog`

### Temporal Schedule (per integration)

| Property | Value |
|----------|-------|
| Frequency | Every 5–60 min (configurable per integration) |
| Overlap | ScheduleOverlapPolicy.SKIP — in-progress runs are not interrupted |
| Jitter | Up to 30s at startup to spread load |
| Cursor | Stored in workflow durable state (before timestamp, Unix ms) — no DB column |
| History | Continue-As-New every 100 pages |
| Lifecycle | Created on integration enable; paused/deleted on enabled = false |

### Pagination (LaunchDarkly)

LD audit log returns max 20 entries per page. No cursor token — walk pages by passing the earliest date in the current page as `before` on the next request. Pagination is sequential; transform workflows are dispatched fire-and-forget per event/flag as Extract completes. Flag detail fetches run in parallel using `workflow.Go` goroutines.

### Fire-and-Forget with Ack

Child workflows use `ParentClosePolicy: ABANDON` so they survive if the parent finishes first. Extract calls `GetChildWorkflowExecution().Get()` to confirm each child started — guarantees delivery without waiting for completion. If a child fails to start, Extract surfaces the error.

---

## 4. Pipeline Flow

### Step 1 — Schedule

`FeatureFlagETLWorkflow` fires on a Temporal schedule (5–60 min) per integration. Routes to the correct provider-specific extract workflow based on `integration.source`. One workflow instance per integration row.

### Step 2 — Extract (LaunchDarklyExtractWorkflow)

Provider-specific. Owns all API calls, pagination logic, and raw data persistence.

- **Fetch audit logs** — `ListAuditLogs` activity calls `GET /auditlog?before=<cursor>&limit=20` sequentially, concatenating all pages until an empty page is returned. The poll cursor (a `before` timestamp) is stored in Temporal's durable workflow state — no DB column needed. Calls Continue-As-New after 100 pages to keep workflow history lean.
- **Fetch flag details** — `GetFlagDetail` activity calls `GET /flags/<project_key>/<flag_key>` for each unique flag key found in the audit logs. Runs in parallel using `workflow.Go` goroutines.
- **Save raw data** — `SaveSourceRecord` activity (shared) persists each raw API response to the `source_records` table before any transformation. This is the permanent, SQL-queryable copy of raw provider data. Each record gets a deterministic key (UUID v5) for cross-table correlation. `source_records` is append-only — data goes in, never changes.
- **Fan out** — For each audit event, spawns a `LaunchDarklyChangelogTransformWorkflow` fire-and-forget. For each unique flag, spawns a `LaunchDarklyFeatureFlagTransformWorkflow` fire-and-forget. Both use `GetChildWorkflowExecution().Get()` to confirm the child started without waiting for completion.

### Step 3 — Transform Changelog (LaunchDarklyChangelogTransformWorkflow)

One per audit event. Two activities:

- `NormalizeChangelog` (provider-specific) — deserializes raw JSON into `LDAuditEntry`, maps to canonical normalized data structure. Handles `titleVerb` → `event_type` mapping, actor extraction, environment inference, rollback detection (flag toggled back within short window), and automated change detection (`token.serviceToken`).
- `SaveChangelog` (shared) — upserts to `feature_flag_changelog`, deduped on `UNIQUE(integration_id, source_key)`.

### Step 4 — Transform Flag (LaunchDarklyFeatureFlagTransformWorkflow)

One per unique flag. Two activities:

- `NormalizeFlagDetail` (provider-specific) — deserializes raw JSON into `LDFlagDetail`, maps to canonical normalized data structure. Extracts flag name, description, environment states, maintainer, lifecycle signals.
- `SaveFeatureFlag` (shared) — upserts to `feature_flags`, deduped on `UNIQUE(key)`. Derives state from the primary environment's enabled status.

### Step 5 — Serve (Search Service)

Read-only. All queries scoped by `org_id`.

- **Structured search** — apply filters (environment, actor, date range) via parameterized SQL and JSONB containment queries (`@>`) over `feature_flag_changelog` and `feature_flags`.
- **Semantic search (post-MVP)** — embed query → ANN search over embeddings → re-rank by recency → return top 20.

### Post-MVP — Enrich

- `FeatureFlagEnrichWorkflow` — fire-and-forget from Transform. Generates embeddings via `EmbedProvider` interface, stores vectors in `feature_flag_embeddings` table.
- `FeatureFlagAnalysisWorkflow` — scheduled (daily). Computes risk scores, stale/dead flag detection, cross-flag associations. Updates `lifecycle.state` in `feature_flags.data` (e.g. `active` → `stale` → `dead`).

### Invariants

- **All writes are idempotent.** Upsert on stable keys throughout; reprocessing any event is always safe.
- **Dedup at the DB layer.** `UNIQUE(key)` on `feature_flags`. `UNIQUE(integration_id, source_key)` on `feature_flag_changelog`.
- **Every query filters by `org_id` first.** No cross-tenant leakage. Ever.
- **Activities are the unit of retry.** Never retry in application code — let Temporal handle it.
- **Extract is rerunnable.** Stateless API calls plus append-only writes to `source_records`.
- **Transform is rerunnable without API calls.** Raw payload lives in Temporal's event history and in `source_records`. Fix a normalization bug, re-trigger transform, zero API calls.
- **Raw data is permanent.** `source_records` is append-only. Canonical tables carry a `source_record_key` reference back to the raw data.
- **Deterministic keys.** Cross-table references use UUID v5. Flag keys are scoped to `org_id|source|project|source_key` — environment is **not** part of the key because all environments are stored in the same flag row (`data.environments`). Changelog keys are scoped to `org_id|source|project|source_key` where `source_key` is the provider's audit event ID. `org_id` is included in all keys to prevent collisions across tenants that share the same source, project, and flag/event identifiers (e.g. two orgs both using LaunchDarkly with the same project name). Both Extract and Transform workflows can independently compute the same key — no coordination required.

---

## 5. Search Service

### Endpoints

| Endpoint | Purpose |
|----------|---------|
| `GET /search` | Semantic (with q) or structured search |
| `GET /flags` | List all flags. Filter by source, environment |
| `GET /flags/:flag_key` | Flag detail + paginated event history |
| `GET /flags/:flag_key/history` | Event history with date range filters |
| `GET /timeline` | Unified event timeline for an org |
| `GET /healthz` | Liveness |
| `GET /metrics` | Prometheus scrape |

### Constraints

- Read-only. Never writes to any table.
- All queries scoped by `org_id`.
- Latency targets: p99 < 500ms structured search.
- Post-MVP: p99 < 2s semantic search; query embedding must use the same model as stored embeddings.

### Security model

The search service is an **internal service** — it is never exposed directly to the public internet. All traffic is routed through an API gateway (Clerk-backed) that verifies the JWT and injects `X-Org-Id` after confirming the org belongs to the authenticated user. The search service **trusts this header** and does not perform its own JWT validation.

**Consequence:** if the API gateway is bypassed (e.g. in local dev or a misconfigured deployment), any caller can impersonate any org. This is an accepted risk for internal services. When the API gateway is wired up, the search service requires no auth changes.

---

## 6. Database Schema

Two PostgreSQL 16 instances: `temporal-postgres` (Temporal internals — do not touch) and `app-postgres` (all tables below).

Five tables: `organizations`, `integrations`, `source_records`, `feature_flags`, `feature_flag_changelog`. No FK constraints between tables — cross-table references use deterministic UUID keys that both Extract and Transform workflows can compute independently.

### Key Conventions

- `id` (bigserial) — internal auto-increment PK on every table
- `key` (uuid UNIQUE) — deterministic UUID v5, used for cross-table correlation
- `org_id`, `integration_id` — reference the `key` column on their respective tables
- `source_key` — provider's identifier, stored for display and source API correlation
- `state` — operational state enum on tables that need it
- `data` (jsonb) — normalized structured data
- `source_data` (jsonb) — raw provider response (only on `source_records`)
- `created_at` / `updated_at` — on all tables (`source_records` has no `updated_at` — append-only)

### Deterministic Keys

Computed with UUID v5:

```go
var DataGillNamespace = uuid.MustParse("...") // hardcoded namespace

// flag_key = uuid5(namespace, "launchdarkly|web-app|enable-checkout-v2")
// Environment is NOT part of the key — all envs are stored in the same row.
func ComputeFlagKey(source, projectKey, sourceFlagKey string) uuid.UUID {
    raw := fmt.Sprintf("%s|%s|%s", source, projectKey, sourceFlagKey)
    return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}

// changelog_key = uuid5(namespace, "launchdarkly|web-app|evt-abc123")
// sourceKey is the provider's audit event ID, unique within a project.
func ComputeChangelogKey(source, projectKey, sourceKey string) uuid.UUID {
    raw := fmt.Sprintf("%s|%s|%s", source, projectKey, sourceKey)
    return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}
```

### `organizations`

Thin indirection layer for auth-provider independence. Clerk/BetterAuth owns name, billing, and membership — local table exists only so a provider swap requires updating `external_org_id` in one place, not every row in every table.

| Column | Type | Notes |
|--------|------|-------|
| id | bigserial | PK |
| key | uuid | UNIQUE — used as org_id across all tables |
| external_org_id | text | Clerk/BetterAuth org ID for JWT correlation |
| created_at | timestamptz | |
| updated_at | timestamptz | |

### `integrations`

One row per provider connection per org. Each row gets its own Temporal Schedule.

| Column | Type | Notes |
|--------|------|-------|
| id | bigserial | PK |
| key | uuid | UNIQUE — used as integration_id across tables |
| org_id | uuid | References organizations.key |
| source | text | `launchdarkly \| unleash \| flagsmith` |
| api_key | text | AES-256-GCM encrypted |
| config | jsonb | project_key, environment_key, poll_interval_minutes, backfill_days, etc. Provider-specific config fields |
| state | text | `active \| paused \| deleted` |
| created_at | timestamptz | |
| updated_at | timestamptz | |

Key indexes: `(org_id)`. `UNIQUE(org_id, source, (config->>'project_key'))`.

### `source_records`

Append-only store of raw provider API responses. Written by Extract before any transformation. Permanent, SQL-queryable reprocessing source. Never modified after insert.

| Column | Type | Notes |
|--------|------|-------|
| id | bigserial | PK |
| key | uuid | UNIQUE — deterministic, for cross-table correlation |
| integration_id | uuid | References integrations.key |
| org_id | uuid | Denormalized for tenant scoping |
| source_type | text | `flag_detail \| audit_log` |
| data | jsonb | Full raw provider response, untouched |
| created_at | timestamptz | |

No `updated_at` — this table is append-only.

Key indexes: `(org_id)`. `(integration_id)`. `(created_at)`.

### `feature_flags`

Current normalized state per flag. Populated by Transform from the provider's GET flag detail endpoint — this is ground truth, not derived from changelog events. Upserted on every ETL run.

| Column | Type | Notes |
|--------|------|-------|
| id | bigserial | PK |
| key | uuid | UNIQUE — deterministic hash(org_id\|source\|project\|source_key) |
| source_record_key | uuid | References source_records.key — traces back to raw data |
| integration_id | uuid | References integrations.key |
| org_id | uuid | Denormalized for tenant scoping |
| state | text | `off \| on \| partial` |
| data | jsonb | Normalized flag data (see Normalized Data Model below) |
| created_at | timestamptz | |
| updated_at | timestamptz | |

Key indexes: `(org_id)`. `(integration_id)`.

### `feature_flag_changelog`

One row per change event. Populated by Transform from the provider's audit log endpoint.

| Column | Type | Notes |
|--------|------|-------|
| id | bigserial | PK |
| key | uuid | UNIQUE — deterministic hash(org_id\|source\|project\|source_key) where source_key is the provider's audit event ID |
| source_record_key | uuid | References source_records.key — traces back to raw data |
| feature_flag_key | uuid | References feature_flags.key — which flag changed |
| integration_id | uuid | References integrations.key |
| org_id | uuid | Denormalized for tenant scoping |
| event_type | text | `created \| updated \| toggled \| deleted \| rollback` |
| state | text | Flag state at time of event |
| occurred_at | timestamptz | Source timestamp — never ingestion time |
| data | jsonb | Normalized event data |
| created_at | timestamptz | |
| updated_at | timestamptz | |

Key indexes: `(org_id, occurred_at DESC)`. `(feature_flag_key, occurred_at DESC)`. `(integration_id)`.

### Normalized Data Model — `feature_flags.data`

All providers normalize into this JSON structure. Typed Go struct serialized as JSONB. Fields not available for a given provider are omitted (nullable).

```json
{
  "name": "Enable checkout v2",
  "description": "New checkout flow for premium users",

  "source": {
    "key": "enable-checkout-v2",
    "provider": "launchdarkly",
    "project": "web-app",
    "created_at": "2024-01-15T10:00:00Z"
  },

  "maintainer": {
    "person": {
      "email": "ariel@acme.com"
    },
    "team": {
      "name": "payments-team"
    }
  },

  "lifecycle": {
    "purpose": "temporary",
    "state": "active"
  },

  "environments": {
    "production": {
      "enabled": true
    },
    "staging": {
      "enabled": true
    },
    "development": {
      "enabled": false
    }
  }
}
```

| Path | Type | Notes |
|------|------|-------|
| name | string | Display name. LD: name, Unleash: name, Flagsmith: feature.name |
| description | string | LD: description, Unleash: description, Flagsmith: feature.description |
| source.key | string | Provider's flag identifier. LD: key, Unleash: name, Flagsmith: feature.name |
| source.provider | string | `launchdarkly \| unleash \| flagsmith` |
| source.project | string | Provider's project/workspace identifier |
| source.created_at | string (ISO 8601) | When the flag was created at the source. Nullable — not all providers expose this |
| maintainer.person.email | string | LD: _maintainer.email. Nullable — Unleash/Flagsmith don't have this |
| maintainer.team.name | string | LD: _maintainerTeam.name. Nullable |
| lifecycle.purpose | string | `temporary \| permanent` |
| lifecycle.state | string | `active \| stale \| dead \| archived \| deprecated` |
| environments.{key}.enabled | bool | Per-environment enabled state. LD: environments[env].on, Unleash: enabled per env, Flagsmith: enabled |

Post-MVP additions (add when needed): `type`, `tags`, `environments.{key}.targeting`, `environments.{key}.variations`.

### Normalized Data Model — `feature_flag_changelog.data`

```json
{
  "flag_name": "Enable checkout v2",
  "description": "Turned on flag",

  "actor": {
    "email": "ariel@acme.com",
    "is_automated": false
  },

  "environment": "production",
  "project": "web-app",
  "is_rollback": false,
  "previous_state": "off",
  "new_state": "active"
}
```

### JSONB Indexing Strategy

Start with no GIN indexes at MVP — hundreds of flags per org will be fast regardless. Add indexes when slow queries appear:

```sql
-- Full column (covers all containment queries at any depth)
CREATE INDEX idx_flags_data ON feature_flags USING GIN (data);

-- Or partial path indexes for specific hot queries
CREATE INDEX idx_flags_env ON feature_flags USING GIN ((data->'environments'));
CREATE INDEX idx_changelog_actor ON feature_flag_changelog USING GIN ((data->'actor'));
```

### Migration

Single file: `001_initialize.sql` — creates `uuid-ossp` extension and all tables above in dependency order. pgvector extension deferred to post-MVP migration.

---

## 7. Open Questions

Do not make implementation decisions that depend on unresolved questions.

| Question | Options / Direction | Needed by |
|----------|---------------------|-----------|
| Statsig / PostHog support | Add to source enum? | Before second provider |
| Backfill concurrency cap | Max concurrent transform workflows during initial 30-day backfill — value TBD | Before backfill impl |
| Weekly digest | Scheduled Temporal workflow vs. separate service | Phase 2 |
| Go DB access layer | sqlc, pgx raw, GORM | LLD session |
| Hosting / compute | Railway, Fly.io, GCP Cloud Run, VPS | Cost analysis |
| Frontend data fetching | TanStack Query, SWR, RSC-only | LLD session |
| PublishEvent downstream | Webhook, Slack, internal bus | Post-MVP |
| feature_flags.data extensions | When to promote type, tags, targeting, variations from post-MVP to typed fields | When P1 features need them |

---

## 8. Roadmap

Do not build these yet. Do not design the MVP in a way that makes them hard to add.

### Future Data Sources

| Source | Category | Questions Unlocked |
|--------|----------|-------------------|
| GitHub | Code | Which PR introduced this flag? Time from commit → prod? |
| PagerDuty | Incidents | Was there an incident open when this flag was rolled back? |
| Datadog | Metrics | Did error rate spike when this flag was enabled? |
| Snowflake | Warehouse | What was conversion rate during this flag's rollout? |
| Jira / Linear | Project | Which ticket drove this flag? |

Future capabilities: natural language querying, Slack/email alerting, public API, cross-source correlation, VP digest.

---

## 9. Repo Structure

```
datagill-core/
├── go.work
├── go.mod
├── Makefile
├── cmd/
│   └── worker/
│       └── main.go              # Temporal worker entry point
├── models/
│   ├── integration.go           # Integration, Source
│   ├── canonical.go             # FeatureFlag, FeatureFlagChangelog, FeatureFlagData, ChangelogData
│   ├── source_record.go         # SourceRecord
│   └── launchdarkly.go          # LDAuditEntry, LDFlagDetail (provider-specific raw types)
├── repository/
│   ├── repository.go            # Repository interface
│   ├── postgres.go              # PostgresRepository struct, constructor, Close
│   ├── postgres_source_records.go   # SaveSourceRecord
│   ├── postgres_feature_flags.go    # SaveFeatureFlag
│   ├── postgres_changelog.go        # SaveChangelog
│   └── postgres_integrations.go     # GetIntegration, ListIntegrations
├── activities/
│   ├── shared.go                # SaveSourceRecord, SaveChangelog, SaveFeatureFlag
│   └── launchdarkly.go          # LD extract + normalize activities
├── workflows/
│   ├── etl.go                   # FeatureFlagETLWorkflow (routes by source)
│   ├── launchdarkly_extract.go  # Pagination, raw data persistence, parallel fetches, fan-out
│   └── launchdarkly_transform.go # Normalize + save (two workflows)
├── services/
│   └── search/                  # Search Service (port: 8082)
└── frontend/                    # Next.js (not yet started)
```

**Adding a new provider (e.g. PostHog):**

- `models/posthog.go` — provider-specific raw types
- `activities/posthog.go` — extract + normalize activities
- `workflows/posthog_extract.go` — pagination, raw data persistence, fan-out
- `workflows/posthog_transform.go` — normalize + save
- One routing case in `workflows/etl.go`
- Register in `cmd/worker/main.go`

Zero changes to existing LaunchDarkly code.

Docker Compose services: `temporal-postgres`, `temporal`, `temporal-ui` (:8080), `app-postgres`, `worker`, `search` (:8082).

---

## 10. Changelog

### v1.0 — 2026-03-18

- Replaced generic workflow hierarchy with provider-specific extract/transform workflows: `LaunchDarklyExtractWorkflow`, `LaunchDarklyChangelogTransformWorkflow`, `LaunchDarklyFeatureFlagTransformWorkflow`
- Added `FeatureFlagETLWorkflow` as scheduled entry point that routes by `integration.source`
- Schema redesign — five tables: `organizations`, `integrations`, `source_records`, `feature_flags`, `feature_flag_changelog`
- Added `source_records` table: append-only raw data store, written by Extract before transformation. Replaces inline `source_data` column on canonical tables
- Canonical tables (`feature_flags`, `feature_flag_changelog`) now use `data jsonb` for normalized structured data, with `source_record_key` reference back to raw data
- Dropped `raw_events` and `normalized_events` tables from previous design
- Promoted `feature_flags` table from open question (P1) to MVP
- Renamed `audit_log` → `changelog` throughout
- New key convention: `id` (bigserial PK) + `key` (deterministic UUID v5) on all tables. No FK constraints — cross-table correlation via shared deterministic keys
- Defined normalized data model for `feature_flags.data`: `name`, `description`, `source`, `maintainer`, `lifecycle`, `environments`
- `lifecycle.purpose` (`temporary | permanent`) and `lifecycle.state` (`active | stale | dead | archived | deprecated`)
- `state` column on tables represents operational state; `lifecycle.state` in data represents health state
- All provider config fields moved to `integrations.config jsonb`
- Auth provider indirection via `organizations.external_org_id` — swap Clerk/BetterAuth by updating one column
- Added fire-and-forget with ack pattern: `GetChildWorkflowExecution().Get()` + `ParentClosePolicy: ABANDON`
- Extract saves raw data to `source_records` before fanning out transforms
- JSONB indexing strategy: start with no GIN indexes, add per-path when needed
- Updated repo structure to reflect provider-specific code organization
- Added post-MVP enrich workflows: `FeatureFlagEnrichWorkflow` (fire-and-forget), `FeatureFlagAnalysisWorkflow` (scheduled daily)

### v0.8 — 2026-03-17

- Renamed workflows to ETL pattern: `FeatureFlagExtractWorkflow`, `FeatureFlagTransformWorkflow`, `NormalizeWorkflow`
- Deferred embeddings and `event_embeddings` table to post-MVP
- Added `source_type` to integrations for workflow dispatch
- Generalized `normalized_events`: `flag_key` → `entity_key`, `flag_name` → `entity_name`, dropped `flag_kind`, `flag_data`, `raw_event`; added metadata/column boundary rule
- Removed `FetchFlagStates` activity (was only feeding `flag_data`)
- Removed `flag_keys` from `raw_events`
- Added `created_at`/`updated_at` to all tables
- Collapsed migrations to single `001_initialize.sql`
- Moved flag detail page into MVP scope
- Updated `feature_flags` open question with P1 direction (derive at MVP, materialize at P1)

### v0.7 — 2026-03-17

- Dropped `flag_cache` table (in-memory dedup within a batch is sufficient)
- Merged `enriched_events` into `normalized_events`
- Thinned `organizations` table (Clerk owns name, billing, membership)
- Added reasoning inline for each schema decision

### v0.6 — 2026-03-13

- Replaced BullMQ + Redis with self-hosted Temporal throughout
- Added `integrations` table; cursor moved to Temporal durable workflow state
- Removed `poll_locks` and `integration_cursors` tables
- Added product requirements section (MVP, P0, P1, P2)

---

## 11. Engineering Standards

Every agent and every team member must follow these standards without exception. They are not guidelines — they are requirements. When making any decision not covered here, consult the relevant section of *Designing Data-Intensive Applications* (DDIA) by Martin Kleppmann and document the decision in this file.

---

### 11.1 Go Server Development

**Goroutine lifecycle**
- Every goroutine must have an owner responsible for starting it, stopping it, and observing its exit
- Use context cancellation to stop goroutines: `select { case <-ctx.Done(): return }`
- Use `sync.WaitGroup` or `golang.org/x/sync/errgroup` to wait for all goroutines before shutdown
- Never start a goroutine inside a short-lived function — pass ownership up or use a supervised manager
- In Temporal workflows: use `workflow.Go` for concurrency — never raw `go` goroutines (Temporal cannot track them)

**Graceful shutdown**

Every server binary must handle SIGINT and SIGTERM identically:
```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
// start servers and workers
<-ctx.Done()
// drain with deadline
shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
server.Shutdown(shutdownCtx)
worker.Stop()
pool.Close()
```
Shutdown order: stop accepting new work → drain in-flight → close DB connections → exit.

**HTTP server — always set timeouts**

Never use `http.DefaultServeMux` or `http.DefaultClient`. Both have no timeouts.
```go
// Inbound server
&http.Server{
    ReadHeaderTimeout: 5 * time.Second,
    ReadTimeout:       30 * time.Second,
    WriteTimeout:      30 * time.Second,
    IdleTimeout:       120 * time.Second,
}

// Outbound client (LD API, etc.)
&http.Client{Timeout: 10 * time.Second}
```

**Interfaces**
- Define interfaces in the *consuming* package, not the implementing package (Go proverb)
- Keep interfaces small: 1–3 methods. Many small interfaces beat one large one
- Do not create an interface until you have ≥2 implementations or need it for testing
- Temporal activities: always register with pointer receivers on a struct — inject dependencies via the struct

**Dependency injection**
- All external dependencies (DB pool, HTTP clients, Temporal client, logger) injected via constructor
- Constructor signature: `func NewXxx(cfg Config, deps ...) (*Xxx, error)` — fail fast if deps are invalid
- Zero package-level mutable `var`. No `init()` functions with side effects

**Error handling**
- Wrap at every package boundary: `fmt.Errorf("NormalizeChangelog: %w", err)`
- Log once, at the site where you *handle* the error (decide what to do). Never log and re-return the same error — it creates duplicate log lines
- Never use `panic` except in `main()` for unrecoverable startup (`log.Fatal` is acceptable there)
- `//nolint:errcheck` requires an inline comment explaining why the error is intentionally ignored

**Concurrency patterns**
- Parallel tasks with error collection: `errgroup.Group` (not goroutines + channel)
- Check context in tight loops: `if err := ctx.Err(); err != nil { return err }`
- Protect shared state with `sync.Mutex` — never access shared state from multiple goroutines without synchronization
- Prefer immutable data passed between goroutines over shared mutable state

---

### 11.2 PostgreSQL

All data decisions are guided by *Designing Data-Intensive Applications* (DDIA). Chapter references are cited inline.

**Column strategy (DDIA Ch. 2 — Data Models)**

Use typed columns for anything you filter, sort, join, or index. Use JSONB for evolving, provider-specific, or flexible payload.

| Typed column | JSONB |
|-------------|-------|
| `org_id`, `integration_id`, `occurred_at` | `data` (normalized payload) |
| `state`, `event_type`, `source`, `key` | `config` (integration config) |
| `source_key`, `feature_flag_key` | Provider-specific sub-fields |

Rule: extend `data` JSONB first; promote a field to a typed column only when query performance requires it, and document the decision in AGENTS.md Section 7.

**Indexing (DDIA Ch. 3 — Storage and Retrieval)**
- B-tree (default): equality and range queries — `org_id`, `occurred_at`, `integration_id`
- GIN: JSONB containment queries (`data @>`) — add only when a slow query is observed and `EXPLAIN ANALYZE` confirms index benefit
- HNSW: vector ANN search — do not build during high-insert backfill (see Section 6 for strategy)
- Rule: run `EXPLAIN (ANALYZE, BUFFERS)` before creating any index. Every index slows writes

**Transactions (DDIA Ch. 7 — Transactions)**
- Use `pgx.Tx` when multiple writes must be atomic
- Keep transactions as short as possible — they hold row locks and block autovacuum
- Never hold a transaction open while waiting for an external system (LD API, Temporal, etc.)
- Default isolation: `READ COMMITTED` (Postgres default) is correct for all current use cases

**Schema evolution (DDIA Ch. 4 — Encoding)**
- JSONB: add fields freely (readers ignore unknown fields — forward compatible)
- Never silently remove or rename a JSONB field — old readers will break
- Rename protocol: add new field → migrate all writers → migrate all readers → remove old field
- All timestamps: `timestamptz` always. Store and compare in UTC. Never `timestamp` without timezone
- Enums stored as `text` — avoid Postgres native `ENUM` type (it is painful to `ALTER TYPE`)

**Derived data and replayability (DDIA Ch. 10–11 — Batch/Stream Processing)**
- `source_records` is the immutable event log — the Kafka equivalent in our architecture
- `feature_flags` and `feature_flag_changelog` are derived/materialized data computed from `source_records`
- Consequence: if normalization logic has a bug, fix the transform workflow and re-run — no data loss, no API calls, no manual intervention
- Every derived row carries `source_record_key` to trace back to the raw input

**Connection pooling**
- Use `pgxpool` exclusively — never `database/sql` with pgx driver
- Recommended config per service:
  - `MaxConns`: 20 (Postgres default max is ~100–150; leave headroom for migrations and admin connections)
  - `MinConns`: 2 (keep warm connections to avoid cold-start latency)
  - `MaxConnIdleTime`: 5 minutes
  - `HealthCheckPeriod`: 1 minute
- Never hold a pool connection outside the scope of a single request or activity

**Query hygiene**
- Never `SELECT *` — always name every column
- All queries on multi-tenant tables start with `WHERE org_id = $1` — enforced by code review and integration tests
- Batch N fetches with `WHERE key = ANY($1::uuid[])` — never issue N separate queries in a loop
- Use named arguments (`pgx` `NamedArgs`) for queries with more than 3 parameters

**Read replicas (future-ready)**
The Search Service is read-only today. When a read replica is added, routing all Search Service queries to it requires only a connection string env var change — no code changes — because the repository layer is already cleanly separated from the handler layer.

---

### 11.3 Monitoring

Follow the **RED method** for services and the **USE method** for infrastructure resources. (DDIA Ch. 1 — Reliability and observability.)

**RED method (every HTTP service and Temporal worker)**
| Signal | What to measure |
|--------|----------------|
| **R**ate | Requests/workflows per second |
| **E**rrors | Rate of 5xx responses; failed workflow/activity executions |
| **D**uration | p50, p95, p99 latency per endpoint/workflow |

**USE method (Postgres, Temporal)**
| Signal | What to measure |
|--------|----------------|
| **U**tilization | Connection pool in use %, DB CPU % |
| **S**aturation | Connection wait queue depth, workflow task queue backlog |
| **E**rrors | Failed queries, activity retries exhausted |

**Metric naming convention**
```
datagill_{service}_{noun}_{unit}_{type}

datagill_search_http_request_duration_seconds   histogram  (labels: method, handler)
datagill_search_http_requests_total             counter    (labels: method, handler, status_code)
datagill_search_db_query_duration_seconds       histogram  (label: query_name)
datagill_worker_workflow_executions_total       counter    (labels: workflow, status)
datagill_worker_activity_duration_seconds       histogram  (label: activity)
```

**Cardinality discipline**
Label values must be bounded. Unbounded label values destroy Prometheus performance.
- Allowed: `handler`, `method`, `status_code` (bucketed: 2xx/4xx/5xx), `workflow`, `activity`, `source`
- Never use as labels: `org_id`, `flag_key`, `user_id`, `request_id`, `integration_id` — these are unbounded

**Histogram buckets — define for your SLO, not Prometheus defaults**
```go
// Search HTTP latency (SLO: p99 < 500ms)
Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1.0, 2.5, 5.0}

// DB query latency
Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5}

// Temporal activity duration (ETL activities can take seconds)
Buckets: []float64{.1, .5, 1.0, 5.0, 10.0, 30.0, 60.0, 300.0}
```

**Alerting philosophy: alert on symptoms, not causes**
- Alert: search service error rate > 1% for 5 minutes → user-visible failure
- Alert: p99 latency > 500ms for 5 minutes → SLO breach
- Do not alert: CPU > 80% → not a user-visible symptom by itself
- Do not alert: Temporal queue depth > N → only alert if correlated with latency or errors

---

### 11.4 Logging

Tool: **zerolog**. All log output is structured JSON. `fmt.Println`, `log.Printf`, and `slog` are forbidden in service code.

**Log levels**

| Level | Use for | Enabled in production? |
|-------|---------|----------------------|
| `TRACE` | Per-iteration loop details | No — compile out |
| `DEBUG` | Verbose per-event details | No — disabled by default |
| `INFO` | Normal operations: request received, workflow started, flag upserted, ETL run complete | Yes |
| `WARN` | Unexpected but handled: 429 retry, unknown titleVerb, slow query | Yes |
| `ERROR` | Operation failed and not recovered: DB write failed, activity exhausted retries | Yes — triggers alert |
| `FATAL` | Unrecoverable startup failure | Yes — `main()` only |

**What to log at INFO**
- Service startup (config summary — never secrets)
- ETL run complete: `integration_id`, pages fetched, records saved, duration
- Workflow started/completed/failed: `workflow_id`, `integration_id`
- HTTP request complete: `method`, `path`, `status`, `duration_ms`, `org_id`, `request_id`

**What to log at WARN**
- Retrying after 429: `integration_id`, attempt number, wait duration
- Unrecognized `titleVerb` in LD audit log: log the raw value (helps catch new LD event types)
- DB query exceeded 100ms: `query_name`, `duration_ms`

**What to never log**
- API keys, tokens, passwords — in any log at any level
- Full HTTP request or response bodies (may contain secrets)
- PII: email addresses at INFO or above (DEBUG only, disabled in production)

**Context propagation pattern**
```go
// Middleware attaches request_id and org_id to the context logger
log := zerolog.Ctx(ctx).With().
    Str("request_id", requestID).
    Str("org_id", orgID).
    Logger()
ctx = log.WithContext(ctx)

// Any downstream code retrieves it — carries all parent fields automatically
zerolog.Ctx(ctx).Info().
    Str("flag_key", key).
    Msg("feature flag upserted")
// Output: {"level":"info","request_id":"...","org_id":"...","flag_key":"...","message":"feature flag upserted"}
```

For Temporal activities: use `activity.GetLogger(ctx)` as the base — it carries workflow and activity metadata. Wrap it with zerolog for consistent JSON format.

**Volume discipline**
High-frequency events (per-audit-log-entry, per-Temporal-activity) must be logged at DEBUG. In production, emit aggregate counts at INFO: `"pages_fetched": 5, "entries_processed": 97`. Never one log line per entry at INFO — log volume must be proportional to operations, not data volume.

---

### 11.5 Clean Code

**Functions**
- Do one thing. If the name contains "and", it is two functions
- If it does not fit on one screen (~40 lines), it is doing too much
- Pure functions (no I/O, no side effects) are preferred — isolate I/O at the boundary
- In this codebase: activities are the I/O boundary. Workflows are pure orchestration. Normalize functions are pure transformations. Keep these roles strict

**Naming**
- Explicit over terse. Scope determines length: `i` is fine in a 3-line loop; package-level functions need descriptive names
- Booleans: always prefix — `isRollback`, `isAutomated`, `shouldEmbed`, `hasEnvironment`
- No abbreviations unless universal: `ctx`, `err`, `id`, `cfg` — yes. `nr`, `flg`, `chg`, `mgr` — no
- Avoid stutter: `models.FeatureFlagModel` → `models.FeatureFlag`. The package name is part of the name

**Comments**
- Comment WHY, not WHAT. The code shows what it does; the comment explains why it does it that way
  - Good: `// LD audit log has no cursor token; we walk pages by passing the earliest timestamp as 'before'`
  - Bad: `// get audit logs`
- Every exported symbol must have a doc comment (enforced by `godot` linter)
- Mark unresolved decisions with a reference: `// TODO(AGENTS.md §7): set rollback detection window`

**Packages**
- A package has one clear responsibility. If you cannot describe it in one sentence, split it
- No circular imports — they indicate a structural design problem
- `internal/` sub-packages for code that must not leak outside the module

**File organization**
- Split files by entity when a single file exceeds ~400 lines or an entity has 3+ methods
- Naming convention: `{implementation}_{entity}.go` — e.g., `postgres_feature_flags.go`, `postgres_integrations.go`
- Keep the main implementation file (`postgres.go`) for struct definition, constructor, Close, and shared helpers
- Go allows methods on a type to be spread across multiple files in the same package — use this to keep files focused
- One file per entity makes it easier to find code, reduces merge conflicts, and keeps each file under ~100 lines

**Avoid**
- Premature abstraction: do not generalize until you have three concrete cases
- Defensive coding for impossible states: do not add `if err != nil` after calling your own pure function that cannot error
- Magic numbers: `const ldMaxPageSize = 20` not the literal `20` in a loop condition
- Deeply nested conditionals: prefer early return over `if { if { if { } } }`

---

### 11.6 DDIA Principles

*Designing Data-Intensive Applications* (Kleppmann) is the primary architectural reference for all storage, consistency, and data flow decisions. When a design decision is not covered by this document, consult the relevant DDIA chapter and add the decision to AGENTS.md Section 7.

**Reliability — design for partial failure (DDIA Ch. 1)**
Any component can fail: LD API, Postgres, Temporal, the network. Every operation must be:
- *Idempotent*: safe to retry without changing the outcome after the first success. This is why every write uses `ON CONFLICT (key) DO UPDATE`.
- *Recoverable*: no manual intervention required to resume after a failure. Temporal's durable execution provides this for ETL.
- *Observable*: failures are visible in logs and metrics before a user reports them.

**Scalability — stateless services (DDIA Ch. 1)**
Worker and Search Service are stateless — all durable state lives in Temporal (workflow state) and Postgres (data). Horizontal scaling requires no coordination and no distributed locking at the application layer.

**Maintainability — three dimensions (DDIA Ch. 1)**
- *Operability*: health endpoints, structured logs, Prometheus metrics, and runbooks for every operational task
- *Simplicity*: prefer the boring solution. Sequential pagination beats concurrent pagination with distributed locking.
- *Evolvability*: adding a provider is 4 files + 1 routing case. The rest of the system is untouched.

**Data models — choose the right tool (DDIA Ch. 2)**
Our hybrid model: relational for structure, integrity, and queryability; JSONB for schema flexibility and provider-specific fields. Do not fight this model — if a JSONB query becomes awkward, promote the field to a typed column.

**Derived data and the immutable log (DDIA Ch. 10–11)**
`source_records` is the immutable log. `feature_flags` and `feature_flag_changelog` are derived views materialized from it. This pattern gives us:
- Free reprocessing: fix a normalization bug, re-run the transform workflow — zero data loss, zero API calls
- Auditability: every derived record links back to its raw source via `source_record_key`
- Independent evolvability: normalization logic changes do not touch raw data

**Temporal ordering is fundamental (DDIA Ch. 8)**
Always record `occurred_at` from the source system. Ingestion time is an implementation detail and must never be used as a proxy for when something happened. Late-arriving events are inserted with their correct `occurred_at` — queries that use `ORDER BY occurred_at` will always produce correct timelines.

**Transactions are not a substitute for Temporal (DDIA Ch. 7 vs. Temporal)**
Temporal's durable execution model handles the reliability guarantees for ETL: each activity is a unit of retry; workflow state persists across failures. Do not wrap Temporal activities in DB transactions to compensate for partial failures — let Temporal retry the activity and rely on idempotent writes.

---

*Last updated: 2026-03-23*
*Status: v1.0 — architecture finalized*
