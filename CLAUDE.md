# CLAUDE.md — DataGill Platform: Source of Truth

> Single source of truth for architecture decisions, product requirements, and open questions.
> Every agent and every PR must trace back to this document.
> Do not implement something that contradicts this document without updating it first.
> **TBD** means the decision has not been made — do not invent one.

---

## 0. North Star

DataGill is a **business intelligence platform for engineering teams**. It connects to the tools engineers use to ship software, normalizes the data into a unified model, and surfaces it through a web UI so teams can answer hard questions about their work.


| Data Source                                      | Questions Unlocked                                              |
| ------------------------------------------------ | --------------------------------------------------------------- |
| Feature flags (LaunchDarkly, Unleash, Flagsmith) | What changed before the incident? How long to ship a flag?      |
| Code (GitHub)                                    | Which PR shipped this flag? Time from commit → flag on in prod? |
| Incidents (PagerDuty)                            | Was there an incident open when this flag was rolled back?      |
| Business metrics (Datadog)                       | Did this flag change correlate with a latency spike?            |
| Data warehouse (Snowflake)                       | What did conversion rate look like when this feature launched?  |


**MVP = feature flag providers only.** Architecture must accommodate future data source categories without rearchitecting the pipeline.

---

## 1. Product Requirements

> "All 3" = LaunchDarkly, Unleash, Flagsmith. "LD" = LaunchDarkly only.

### MVP — Lights On


| Feature                                | Source | Notes                                            |
| -------------------------------------- | ------ | ------------------------------------------------ |
| Activity feed (who changed what, when) | All 3  | LD `titleVerb` is cleanest canonical description |
| Flag status (off / on / partial)       | All 3  | LD direct; Unleash/Flagsmith need inference      |
| Trend line (changes over time)         | All 3  | Aggregate by day                                 |
| Actor attribution (last modified by)   | All 3  | Normalize to `actor_email`                       |
| Search by flag name / time range       | All 3  | LD supports full-text; others: time range only   |
| Flag detail page                       | All 3  | Current state + paginated event history per flag |


### P0 — Root Cause Analysis


| Feature                                            | Source | Notes                                                  |
| -------------------------------------------------- | ------ | ------------------------------------------------------ |
| "What changed in the last X hours?" scoped to prod | All 3  | Filter by environment + time window                    |
| Automated vs. human change detection               | LD     | `token.serviceToken` field distinguishes API vs. human |
| Rollback detection                                 | All 3  | Flag toggled back within short window                  |
| Per-flag timeline view                             | All 3  | Paginated history per flag, newest first               |


### P1 — Developer Velocity


| Feature                                        | Source  | Notes                              |
| ---------------------------------------------- | ------- | ---------------------------------- |
| Time from flag creation → on in prod           | All 3   | Requires `first_seen_at` per flag  |
| Stale flag list (created, never launched)      | All 3   | No "enabled" event past X days     |
| Dead flag list (launched 60+ days, no changes) | All 3   | Compare `last_event_at`            |
| Flag cleanup suggestions                       | Derived | Combine stale + dead signals       |
| Owner-level velocity                           | All 3   | Group by actor across flag history |


### P1 — Monthly Snapshot


| Feature                    | Source  | Notes                                      |
| -------------------------- | ------- | ------------------------------------------ |
| Flags launched this month  | All 3   | Count enable/100% events                   |
| Flags cleaned up (deleted) | All 3   | Count delete events                        |
| Monthly digest             | Derived | AI-generated narrative is a P2 enhancement |


### P2 — Intelligence


| Feature                         | Notes                                                              |
| ------------------------------- | ------------------------------------------------------------------ |
| Anomaly: high change frequency  | Flag changed >N times in 24h                                       |
| Natural language search         | LLM over stored embeddings — do not rely on source `q` param alone |
| Risk score                      | Blast radius × recent change rate                                  |
| AI narrative summaries per flag | LLM over flag event history                                        |


---

## 2. Tech Stack

### Decided


| Layer         | Choice                                                                | Rationale                                                                           |
| ------------- | --------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| Frontend      | Next.js (App Router) + shadcn/ui                                      | Best-in-class SSR; accessible components we own                                     |
| Backend       | Go + Chi                                                              | Goroutines fit ETL concurrency; Chi is lightweight and stdlib-compatible            |
| Database      | PostgreSQL 16 + pgvector                                              | ACID, JSONB, vector search. Two instances: one for Temporal state, one for app data |
| Auth          | Clerk                                                                 | JWT issuance + org management; Go validates via JWKS                                |
| Embeddings    | OpenAI text-embedding-3-small / Ollama nomic-embed-text               | **Post-MVP.** Swappable via `EmbedProvider` interface                               |
| Orchestration | Self-hosted Temporal                                                  | Replaces queues. Durable state, retry, scheduling in one primitive                  |
| Migrations    | golang-migrate                                                        | Run at startup or as a separate job                                                 |
| Metrics       | Prometheus + Grafana                                                  | Scrape endpoint at `/metrics` on all services                                       |
| Secrets       | AWS Secrets Manager (cloud) / env vars + Docker secrets (self-hosted) |                                                                                     |
| Provider auth | API key (MVP)                                                         | Customer pastes read-only key; encrypted at rest with AES-256-GCM                   |
| Webhooks      | None in v1 — poll only                                                | No inbound public endpoints; customers provide API key                              |


### TBD


| Decision               | Options                             | Blocking On   |
| ---------------------- | ----------------------------------- | ------------- |
| Go DB access layer     | sqlc, pgx raw, GORM                 | LLD session   |
| Hosting / compute      | Railway, Fly.io, GCP Cloud Run, VPS | Cost analysis |
| Frontend data fetching | TanStack Query, SWR, RSC-only       | LLD session   |
| Provider auth V2       | OAuth (no manual key management)    | Post-MVP      |


---

## 3. Architecture

Two runtime components: a **Worker** (Temporal, handles all ETL) and a **Search Service** (HTTP query layer).

**Data flow**


| Step                     | Component                              |
| ------------------------ | -------------------------------------- |
| 1. Poll source APIs      | LaunchDarkly / Unleash / Flagsmith     |
| 2. Orchestrate workflows | Temporal (self-hosted) — Worker binary |
| 3. Store and query data  | PostgreSQL app DB                      |
| 4. Serve queries         | Search Service :8082                   |
| 5. Display to users      | Frontend                               |


**Temporal workflow hierarchy**

- **FeatureFlagExtractWorkflow** — one per integration, runs on schedule (Extract)
  - **FeatureFlagTransformWorkflow** — one per page of audit log results (Transform + Load)
    - FetchAuditLogPage
    - WriteRawEvents
    - **NormalizeWorkflow** — one per raw event (fire-and-forget)
      - NormalizeEvent
      - *(Post-MVP: GenerateEmbedding, StoreEmbedding)*

**Tables written to**

organizations · integrations · raw\_events · normalized\_events

### Temporal Schedule (per integration)


| Property  | Value                                                                         |
| --------- | ----------------------------------------------------------------------------- |
| Frequency | Every 5 min                                                                   |
| Overlap   | `ScheduleOverlapPolicy.SKIP` — in-progress runs are not interrupted           |
| Jitter    | Up to 30s at startup to spread load                                           |
| Cursor    | Stored in workflow durable state (`before` timestamp, Unix ms) — no DB column |
| History   | `Continue-As-New` every 100 pages                                             |
| Lifecycle | Created on integration enable; paused/deleted on `enabled = false`            |


### Pagination (LaunchDarkly)

LD audit log returns max **20 entries per page**. No cursor token — walk pages by passing the earliest `date` in the current page as `before` on the next request. `FeatureFlagTransformWorkflow` is dispatched **fire-and-forget** per page (fetching is sequential; normalization is parallel). Cap concurrent `FeatureFlagTransformWorkflow`s during backfill to avoid fan-out.

---

## 4. Pipeline Flow

### Step 1 — Schedule

FeatureFlagExtractWorkflow fires every 5 min per integration. The poll cursor (a `before` timestamp) is stored in Temporal's durable workflow state — no DB column needed.

### Step 2 — Fetch page *(FetchAuditLogPage)*

Calls `GET /auditlog?before=<cursor>&limit=20`. Spawns a FeatureFlagTransformWorkflow fire-and-forget for the page, then advances the cursor to the earliest date in the page and repeats until the page is empty. Calls Continue-As-New after 100 pages to keep workflow history lean.

### Step 3 — Write raw events *(WriteRawEvents)*

Writes one `raw_events` row per audit entry, deduped on `UNIQUE(integration_id, source_event_id)`. Spawns a NormalizeWorkflow fire-and-forget per event.

### Step 4 — Normalize *(per event)*

**NormalizeEvent** maps the raw payload to canonical schema, extracts entity name from the audit entry, and detects rollbacks and automated changes. Writes to `normalized_events`.

> **Post-MVP:** GenerateEmbedding calls EmbedProvider for a 1536-dim vector; StoreEmbedding writes to `event_embeddings`.

### Step 5 — Serve *(Search Service)*

- **Structured search** — apply filters (environment, actor, date range) via parameterized SQL over `normalized_events`.
- **Semantic search (post-MVP)** — embed query → ANN search over embeddings → re-rank by recency → return top 20.

### Invariants

- **Raw data is never deleted.** `raw_events` is the only copy of source payloads. Reprocess from here on failure.
- **All writes are idempotent.** Upsert on stable keys throughout; reprocessing any event is always safe.
- **Dedup at the DB layer.** `UNIQUE(integration_id, source_event_id)` on `raw_events`.
- **Every query filters by `org_id` first.** No cross-tenant leakage. Ever.
- **Activities are the unit of retry.** Never retry in application code — let Temporal handle it.

---

## 5. Search Service

**Endpoints**


| Endpoint                         | Purpose                                             |
| -------------------------------- | --------------------------------------------------- |
| `GET /search`                    | Structured search; post-MVP: semantic with `q` param |
| `GET /flags`                     | List all flags. Filter by source, environment       |
| `GET /flags/:entity_key`         | Flag detail + paginated event history               |
| `GET /flags/:entity_key/history` | Event history with date range filters               |
| `GET /timeline`                  | Unified event timeline for an org                   |
| `GET /healthz`                   | Liveness                                            |
| `GET /metrics`                   | Prometheus scrape                                   |


**Constraints**

- Read-only. Never writes to any table.
- All queries scoped by `org_id`.
- Latency targets: p99 < 500ms structured search.
- Post-MVP: p99 < 2s semantic search; query embedding must use the same model as stored embeddings.

---

## 6. Database Schema

Two PostgreSQL 16 instances: `temporal-postgres` (Temporal internals — do not touch) and `app-postgres` (all tables below).

### `organizations`

One row per customer tenant. FK anchor for all other tables. Kept thin — Clerk owns name, billing, and membership; we only store what we need for referential integrity and webhook correlation. Created when Clerk fires the `organization.created` webhook.


| Column       | Type        | Notes                                            |
| ------------ | ----------- | ------------------------------------------------ |
| id           | uuid        | PK — used as `org_id` FK throughout              |
| clerk_org_id | text        | Clerk's org ID; used to correlate webhook events |
| created_at   | timestamptz |                                                  |
| updated_at   | timestamptz |                                                  |


Key indexes: `UNIQUE(clerk_org_id)`.

### `integrations`

One row per provider connection per org. Each row gets its own Temporal Schedule. `source_type` determines which workflow is dispatched when the schedule fires.


| Column                  | Type        | Notes                                                                          |
| ----------------------- | ----------- | ------------------------------------------------------------------------------ |
| id                      | uuid        | PK                                                                             |
| org_id                  | uuid        | FK → organizations                                                             |
| source                  | enum        | `launchdarkly \| unleash \| flagsmith \| github \| pagerduty`                  |
| source_type             | enum        | `feature_flag \| code \| incident \| metrics \| warehouse` — determines which workflow is dispatched |
| api_key                 | text        | AES-256-GCM encrypted                                                          |
| project_key             | text        | Source system project identifier                                               |
| environment_key         | text        | Null = track all environments                                                  |
| poll_interval_minutes   | int         | Default 60                                                                     |
| backfill_days           | int         | Default 30                                                                     |
| enabled                 | bool        | Default true                                                                   |
| created_at              | timestamptz |                                                                                |
| updated_at              | timestamptz |                                                                                |


Key indexes: `(org_id)`. `UNIQUE(org_id, source, project_key)`.

### `raw_events`

One row per audit log event. Only copy of raw source payloads — never deleted. Kept despite Temporal storing activity inputs: Temporal history has a 50MB/workflow size limit, a short configurable retention window, and is not queryable by SQL. `raw_events` is the permanent, queryable reprocessing source — e.g. re-embed all events after switching models.


| Column          | Type        | Notes                             |
| --------------- | ----------- | --------------------------------- |
| id              | uuid        | PK                                |
| integration_id  | uuid        | FK → integrations                 |
| org_id          | uuid        | Denormalized for query efficiency |
| source_event_id | text        | Stable ID from source API         |
| raw_json        | jsonb       | Full raw payload                  |
| created_at      | timestamptz |                                   |
| updated_at      | timestamptz |                                   |


Key indexes: `UNIQUE(integration_id, source_event_id)`. `(org_id)`. `(created_at)`.

### `normalized_events`

Canonical schema, decoupled from source API shape. Queried by Search Service. Generic column names (`entity_key`, `entity_name`) support data sources beyond feature flags without schema changes.

**Column vs. metadata boundary:** A field is a column if the Search Service filters or sorts on it. Everything else goes in `metadata` jsonb.


| Column                 | Type        | Notes                                                              |
| ---------------------- | ----------- | ------------------------------------------------------------------ |
| id                     | uuid        | PK                                                                 |
| raw_event_id           | uuid        | FK → raw_events                                                    |
| entity_key             | text        | Stable identifier from source (e.g. flag key, PR number)           |
| integration_id         | uuid        | FK → integrations                                                  |
| org_id                 | uuid        | Denormalized                                                       |
| event_type             | text        | `flag.created \| flag.updated \| flag.deleted \| targeting.changed` |
| actor_id               | text        | Normalized from source                                             |
| actor_email            | text        | Normalized from source                                             |
| entity_name            | text        | Display name extracted from audit log entry                        |
| project_key            | text        |                                                                    |
| environment_key        | text        | Default 'production'                                               |
| occurred_at            | timestamptz | Source timestamp — never ingestion time                            |
| metadata               | jsonb       | Fields not filtered by Search Service: `is_rollback`, `is_automated`, source-specific detail |
| created_at             | timestamptz |                                                                    |
| updated_at             | timestamptz |                                                                    |


Key indexes: `UNIQUE(raw_event_id, entity_key)`. `(org_id, environment_key, occurred_at DESC)`. `(entity_key)`. `(org_id, actor_email, occurred_at DESC)`.

> **Post-MVP:** Add `event_embeddings` table with `vector(1536)` column and HNSW index for semantic search. Build the index in one `CREATE INDEX` pass after bulk-inserting all vectors — not incrementally during backfill.

### Migration

Single file: `001_initialize.sql` — creates all extensions (`uuid-ossp`, `vector`) and all tables above in dependency order.

---

## 7. Open Questions

> Do not make implementation decisions that depend on unresolved questions.


| Question                                | Options / Direction                                                                                                                       | Needed by              |
| --------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ---------------------- |
| `feature_flags` entity table            | At MVP: derive `first_seen_at` / `last_event_at` from `normalized_events` on query. At P1: materialize into a `feature_flags` table when query latency becomes a concern. | Before P1              |
| Statsig / PostHog support               | Add to `source` enum?                                                                                                                     | Before second provider |
| `FeatureFlagTransformWorkflow` concurrency cap | Max in-flight during backfill — value TBD                                                                                          | Before backfill impl   |
| Weekly digest                           | Scheduled Temporal workflow vs. separate service                                                                                          | Phase 2                |
| Go DB access layer                      | sqlc, pgx raw, GORM                                                                                                                       | LLD session            |
| Hosting / compute                       | Railway, Fly.io, GCP Cloud Run, VPS                                                                                                       | Cost analysis          |
| `PublishEvent` downstream               | Webhook, Slack, internal bus                                                                                                              | Post-MVP               |


---

## 8. Roadmap

Do not build these yet. **Do not design the MVP in a way that makes them hard to add.**


| Source        | Category  | Questions Unlocked                                         |
| ------------- | --------- | ---------------------------------------------------------- |
| GitHub        | Code      | Which PR introduced this flag? Time from commit → prod?    |
| PagerDuty     | Incidents | Was there an incident open when this flag was rolled back? |
| Datadog       | Metrics   | Did error rate spike when this flag was enabled?           |
| Snowflake     | Warehouse | What was conversion rate during this flag's rollout?       |
| Jira / Linear | Project   | Which ticket drove this flag?                              |


Future capabilities: natural language querying (post-MVP embeddings), Slack/email alerting, public API, cross-source correlation, VP digest.

---

## 9. Repo Structure

```
datagill-core/
├── go.work
├── Makefile
├── shared/           # shared Go types, EmbedProvider interface
├── services/
│   ├── worker/       # Temporal worker (port: none — connects to Temporal server)
│   └── search/       # Search Service (port: 8082)
└── frontend/         # Next.js (not yet started)
```

**Docker Compose services:** `temporal-postgres`, `temporal`, `temporal-ui` (:8080), `app-postgres`, `worker`, `search` (:8082).

---

*Last updated: 2026-03-17 — v0.8: ETL workflow naming; remove embeddings to post-MVP; generalize normalized_events; remove flag_data/flag_keys; source_type on integrations; single migration; flag detail in MVP*
