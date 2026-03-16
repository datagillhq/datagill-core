# CLAUDE.md — DataGill Platform: Source of Truth

> This document is the **single source of truth** for architecture decisions, product requirements, and open questions.
> Every agent and every PR must trace back to this document.
> Do not implement something that contradicts this document without updating it first.
> When a section says **TBD**, that means the decision has NOT been made — do not invent one.

---

## 0. The North Star

DataGill is a **business intelligence platform for engineering teams**. It connects to the tools engineers use to ship software — feature flag providers, business metrics, data warehouses, source control — normalizes all of that data into a unified model, and surfaces it through a web UI so teams can answer hard questions about their work.

The questions DataGill answers grow with each data source integrated:

| Data Source | Questions Unlocked |
|-------------|-------------------|
| Feature flags (LaunchDarkly, Unleash, Flagsmith) | What changed before the incident? How long does it take us to ship a flag? |
| Code (e.g. GitHub) | Which PR shipped this flag? How long from commit to flag enabled in production? |
| Incidents (e.g. PagerDuty) | Was there an incident open when this flag was rolled back? |
| Business metrics (e.g. Datadog) | Did this flag change correlate with a latency spike? |
| Data warehouse (e.g. Snowflake) | What did our conversion rate look like when this feature launched? |

**MVP = feature flag providers only.** The architecture must be designed from day one to accommodate additional data source categories without rearchitecting the pipeline.

The system is an **ETL pipeline** with a semantic search and query layer on top. Each new data source is a new integration — the pipeline, storage, and query layers stay the same.

---

## 1. Product Requirements

Features are organized by tier. MVP is the launch bar. P0/P1/P2 are post-launch priorities.

> **Data source notes:** "LD" = LaunchDarkly. "All 3" = LaunchDarkly, Unleash, Flagsmith (current MVP providers). Where notes reference Statsig or PostHog, those observations are carried forward from earlier API research but those providers are not in the current `integration_source` enum — see §9 open questions.

### MVP — "Lights On"

All data directly available from feature flag audit log APIs. No inference required for LaunchDarkly; Unleash/Flagsmith may require lightweight inference for some fields.

| Feature | Data Source | Notes |
|---------|-------------|-------|
| Activity feed (who changed what, when) | All 3 | LD's `titleVerb` is cleanest; use as canonical change description |
| Flag status breakdown (off / on / partial) | LD direct; Unleash/Flagsmith need inference | LD `titleVerb` reliably encodes enable/disable state |
| Trend line (changes over time) | All 3 | Aggregate by date using `occurred_at` |
| Actor attribution ("last modified by") | All 3 | LD has `member.email`; normalize to `actor_email` in `normalized_events` |
| Basic search by flag name / time range | All 3 | LD supports `q` param + `before`/`after`; others: time range only |

### P0 — Root Cause Analysis

| Feature | Data Source | Notes |
|---------|-------------|-------|
| "What changed in the last X hours?" scoped to prod | All 3 | Filter `normalized_events` by `environment_key = 'production'` + `occurred_at > now() - interval` |
| Automated vs. human change detection | LD: `token.serviceToken` field | Flag changes made by API token vs. human login. Store as boolean in `metadata` |
| Rollback detection | All 3 | Flag toggled back within short window — requires processing logic in `NormalizeEvent` activity |
| Per-flag timeline view | All 3 | Paginated history per `flag_key`, ordered by `occurred_at DESC` |

### P1 — Developer Velocity

| Feature | Data Source | Caveat |
|---------|-------------|--------|
| Time from flag creation → turned on in prod | All 3 | Compare `first_seen_at` vs. first `occurred_at` where event_type = enabled-in-prod. Requires storing `first_seen_at` per flag |
| Stale flag list (created but never launched) | All 3 | Flags with no "enabled" event past X days |
| Dead flag list (launched 60+ days, never changed) | All 3 | `last_event_at` comparison |
| Flag cleanup suggestions | Derived | Combine stale + dead signals |
| Owner-level velocity | All 3 | Group by `actor_email` across paginated flag history |

### P1 — Company Progress / Monthly Snapshot

| Feature | Data Source | Notes |
|---------|-------------|-------|
| Flags launched this month | All 3 | Count events where `event_type` = enabled-to-100% or equivalent |
| Flags cleaned up (deleted) | All 3 | Count `event_type = flag.deleted` |
| Monthly digest summary | Derived | Aggregated counts; AI-generated narrative is a P2 enhancement |

### P2 — Intelligence Layer

| Feature | Data Source | Notes |
|---------|-------------|-------|
| Anomaly: unusually high change frequency | Derived | Flag changed >N times in 24h |
| Natural language search | LLM layer over `event_embeddings` | Do not rely on source `q` param alone — it is keyword-only |
| Risk score (blast radius × recent change) | LD: flag metadata (targeting %) + audit log | Both available from LD API |
| Narrative AI summaries per flag | LLM over `normalized_events` history | Feasible with Claude over stored event history |

---

## 2. Technology Decisions

### Decided

| Layer | Choice | Rationale |
|-------|--------|-----------|
| Frontend | Next.js (App Router) + shadcn/ui | Best-in-class React SSR, shadcn gives us accessible, unstyled components we own |
| Backend language | **Go** | Goroutines are a natural fit for the ETL workload — concurrent provider polling, parallel normalization jobs, low memory per goroutine |
| Go HTTP framework | **Chi** | Lightweight, idiomatic, stdlib-compatible. Easy to add middleware and route groups as endpoints grow |
| Database | **PostgreSQL 16 + pgvector** | ACID guarantees, JSONB for raw payload flexibility, pgvector for semantic search embeddings. Two DB instances: one for Temporal internal state, one for application data |
| Auth (platform) | **Clerk** | Handles user login, org management, and JWT issuance. Language-agnostic — Go validates Clerk JWTs via JWKS endpoint |
| Embedding model | **OpenAI text-embedding-3-small** (cloud) / **Ollama nomic-embed-text** (self-hosted) | Swappable via EmbedProvider interface — same interface, different implementation |
| Workflow orchestration | **Self-hosted Temporal** | Replaces BullMQ + Redis. Durable workflow state eliminates cursor tables and poll lock tables. Retry, scheduling, and concurrency handled by Temporal primitives |
| Metrics | **Prometheus + Grafana Cloud** (cloud) / **Prometheus + Grafana** (self-hosted) | Standard scrape endpoint at `/metrics` on all services |
| Secret management | **AWS Secrets Manager** (cloud) / **env vars + Docker secrets** (self-hosted) | |
| DB migrations | **golang-migrate** | Applied at service startup or as a separate migration job |
| Search Service auth | **Clerk JWT** | Frontend passes Clerk JWT; Search Service validates via JWKS. No per-resource authorization yet — everyone is admin. Permissions TBD post-MVP |
| Organisation creation | **Clerk webhook** | An `organisations` row is created on business onboarding when the Clerk org is provisioned. Clerk org ID is the primary external reference |
| Provider auth (MVP) | **API key** | Customer generates read-only API key in their provider, pastes into DataGill. Encrypted at rest with AES-256-GCM |
| Search backend | **PostgreSQL + pgvector** | HNSW index for ANN semantic search over `event_embeddings`. Structured search via parameterized SQL over `normalized_events` |
| Webhooks (v1) | **None — poll only** | No inbound public endpoints in v1. Customers provide an API key; we poll on a schedule |
| Flag detail cache | **PostgreSQL `flag_cache` table** | Replaces Redis. TTL enforced inline with `WHERE fetched_at > now() - interval '5 minutes'`. Stale rows vacuumed nightly by `pg_cron` |

### Not Decided Yet (TBD)

| Decision | Options Being Considered | Blocking On |
|----------|--------------------------|-------------|
| Go DB access layer | sqlc, pgx raw, GORM | LLD session |
| Hosting / compute | Railway, Fly.io, GCP Cloud Run, self-hosted VPS | Cost analysis |
| Frontend state / data fetching | TanStack Query, SWR, RSC-only | LLD session |
| Provider auth (V2) | OAuth flow so customers don't manage keys manually | Post-MVP |

---

## 3. Architecture Overview

The platform has two runtime components: a **Worker** (Temporal worker running all ingestion + processing activities) and a **Search Service** (HTTP query layer). They share a PostgreSQL application database.

```
  Customer tools (LaunchDarkly, Unleash, GitHub, PagerDuty)
         |
         |  Poll-based (audit log API + flag detail fetch)
         v
  +--------------------------------------------------+
  |  Temporal Server (self-hosted)                   |
  |                                                  |
  |  IntegrationIngestWorkflow  ← Schedule/integration|
  |    └─ EnrichBatchWorkflow (per page)             |
  |         ├─ FetchAuditLogPage activity            |
  |         ├─ CheckFlagCache activity               |
  |         ├─ BatchFetchFlags activity              |
  |         ├─ WriteFlagCache activity               |
  |         └─ ExplodeAndWrite activity              |
  |              └─ NormalizeAndEmbedWorkflow        |
  |                   ├─ NormalizeEvent activity     |
  |                   ├─ GenerateEmbedding activity  |
  |                   ├─ StoreEmbedding activity     |
  |                   └─ PublishEvent activity       |
  +--------------------------------------------------+
         |
         | writes to app DB
         v
  +-----------------------------------------------+
  |  PostgreSQL (app DB)                          |
  |  raw_events · flag_cache · enriched_events    |
  |  normalized_events · event_embeddings         |
  +-----------------------------------------------+
         |
         v
  +---------------------------+
  |   Search Service  :8082   |
  |  SemanticSearchHandler    |
  |  StructuredQueryHandler   |
  |  EmbedProvider            |
  +---------------------------+
         |
         v
    Frontend / API consumers
```

### Temporal Scheduling Details

| Property | Value |
|----------|-------|
| Trigger | Temporal Schedule, every 5 min **per integration** (one schedule per `integrations` row) |
| Overlap policy | `ScheduleOverlapPolicy.SKIP` — if a run is still in progress, the next scheduled fire is skipped |
| Startup jitter | Up to 30s per integration to avoid thundering herd on source APIs |
| Cursor state | Lives inside `IntegrationIngestWorkflow` durable state — no cursor column in the DB |
| History pruning | `Continue-As-New` called after every 100 pages to keep event history under Temporal limits (50 MB / 51,200 events) |
| Schedule lifecycle | Created when an integration is enabled; paused/deleted when `integrations.enabled = false` |

### Service / Component Responsibilities

| Component | Port | Owns | Does NOT own |
|-----------|------|------|--------------|
| Worker (Temporal) | — | Polling, raw storage, enrichment, normalization, embeddings | HTTP query serving |
| Search Service | 8082 | Serving semantic + structured search queries | Writing to any table |
| Frontend | — | UI rendering, data display, user interaction | Business logic, direct DB access |

---

## 4. ETL Pipeline — The Flow

```
Step 1: SCHEDULE
  └─ Temporal Schedule fires IntegrationIngestWorkflow every 5 min per integration
     Jitter (up to 30s) applied per integration to spread load
     ScheduleOverlapPolicy.SKIP: concurrent runs for same integration are dropped
     Schedule created/paused/deleted in sync with integrations.enabled

Step 2: FETCH AUDIT LOG PAGE
  └─ FetchAuditLogPage activity — GET /auditlog?integration=X&cursor=Y
     Cursor lives in IntegrationIngestWorkflow durable state (not DB)
     One EnrichBatchWorkflow spawned per page of results
     Continue-As-New after every 100 pages

Step 3: ENRICH BATCH (per page)
  └─ CheckFlagCache activity
       — lookup (integration_id, environment_key, flag_key) in flag_cache
       — TTL check: WHERE fetched_at > now() - interval '5 minutes'
     BatchFetchFlags activity
       — for cache misses: GET /flags from source API, scoped per environment
       — flag_keys extracted at FetchAuditLogPage to avoid re-parsing payloads
     WriteFlagCache activity
       — UPSERT flag_cache rows (integration_id, environment_key, flag_key)
     ExplodeAndWrite activity
       — write raw_events row (one per audit log event), dedup on UNIQUE(integration_id, source_event_id)
       — explode to enriched_events rows (one per raw_event_id × flag_key pair)
       — spawn one NormalizeAndEmbedWorkflow per enriched_events row

Step 4: NORMALIZE & EMBED (per enriched_events row)
  └─ NormalizeEvent activity
       — map raw audit log entry + flag snapshot → canonical normalized_events schema
       — detect rollback (flag toggled back within short window) → set metadata.is_rollback
       — detect automated change (token.serviceToken present) → set metadata.is_automated
  └─ GenerateEmbedding activity
       — call EmbedProvider (OpenAI or Ollama)
       — produce vector(1536)
  └─ StoreEmbedding activity
       — write to event_embeddings with model name + input_text for debugging
  └─ PublishEvent activity
       — notify downstream (webhook, Slack, etc. — TBD)

Step 5: SERVE
  └─ Search Service receives query from frontend
     Semantic: embed query → ANN search over event_embeddings → re-rank by recency → return top 20
     Structured: parse filters → parameterized SQL over normalized_events → return results
```

### Critical Invariants (never violate these)

- **Raw data is never discarded.** `raw_events` is the only copy of raw source payloads. Never delete. Failed enrichment retries from this table.
- **Deduplication at the DB layer.** `UNIQUE(integration_id, source_event_id)` on `raw_events` — safe to receive the same event twice.
- **Every query filters by `org_id` first.** No cross-tenant data leakage. Ever.
- **Enriched rows are idempotent.** `enriched_events` upserts on `(raw_event_id, flag_key)` PK — re-running enrichment is always safe.
- **Webhook handlers return immediately (future).** Never do synchronous processing inside a webhook handler. Store raw, dispatch workflow, return 200.
- **All writes must be idempotent.** Reprocessing any event must be safe.
- **`flag_cache` is scoped per integration, not per org.** Key is `(integration_id, environment_key, flag_key)` — flag keys are not globally unique across sources; two providers can use the same key string.
- **Temporal activities are the unit of retry.** Do not retry inside application code. Failed activities are retried by Temporal with configured backoff.

---

## 5. Worker (Temporal Activities)

The Worker registers all activity and workflow implementations. It is a single binary scaled via replicas.

### Workflows

| Workflow | Trigger | Responsibility |
|----------|---------|---------------|
| `IntegrationIngestWorkflow` | Temporal Schedule (every 5 min **per integration**) | Outer loop. Manages cursor in durable state. Spawns `EnrichBatchWorkflow` per page. Calls `Continue-As-New` after 100 pages |
| `EnrichBatchWorkflow` | Spawned by `IntegrationIngestWorkflow` | Per-page enrichment. Runs cache check, flag fetch, write, and explode activities. Spawns `NormalizeAndEmbedWorkflow` per row |
| `NormalizeAndEmbedWorkflow` | Spawned by `EnrichBatchWorkflow` | Per-row normalization, embedding, and storage |

### Activities

| Activity | Workflow | Responsibility |
|----------|----------|---------------|
| `FetchAuditLogPage` | `EnrichBatchWorkflow` | Paginate source audit log API. Returns ordered entries + next cursor |
| `CheckFlagCache` | `EnrichBatchWorkflow` | Lookup `(integration_id, environment_key, flag_key)` in `flag_cache`. TTL: 5 min |
| `BatchFetchFlags` | `EnrichBatchWorkflow` | Fetch full flag state from source API for cache misses. Scoped per environment |
| `WriteFlagCache` | `EnrichBatchWorkflow` | UPSERT `flag_cache` rows keyed on `(integration_id, environment_key, flag_key)` |
| `ExplodeAndWrite` | `EnrichBatchWorkflow` | Write `raw_events` (dedup on `UNIQUE(integration_id, source_event_id)`). Explode to `enriched_events` (one row per raw_event_id × flag_key) |
| `NormalizeEvent` | `NormalizeAndEmbedWorkflow` | Map raw payload + flag snapshot → canonical `normalized_events` schema. Detects rollbacks and automated changes |
| `GenerateEmbedding` | `NormalizeAndEmbedWorkflow` | Call EmbedProvider. Returns vector(1536) |
| `StoreEmbedding` | `NormalizeAndEmbedWorkflow` | Write to `event_embeddings` |
| `PublishEvent` | `NormalizeAndEmbedWorkflow` | Notify downstream (TBD) |

### Constraints

- Back off on 429, respect `Retry-After` headers from source APIs — configure as Temporal activity retry policy
- Cursor state is in `IntegrationIngestWorkflow` durable state — no cursor columns in the DB
- Poll concurrency is managed by Temporal (`ScheduleOverlapPolicy.SKIP`) — no poll lock table needed
- Onboarding backfill: start `IntegrationIngestWorkflow` with cursor set to `now() - backfill_days` — same code path as live polling
- Each `integrations` row gets its own independent Temporal Schedule — adding a second integration for an org is purely additive

---

## 6. Search Service

### Components

| Component | Responsibility |
|-----------|---------------|
| `SearchService` | Entry point. HTTP server, routing, middleware, graceful shutdown |
| `SemanticSearchHandler` | Natural language queries. Embeds query, runs ANN search over `event_embeddings`, re-ranks, returns results |
| `StructuredQueryHandler` | Filtered queries by source, date range, actor, environment. Queries `normalized_events` |
| `EmbedProvider` | Same interface as Worker. Must use same model as stored embeddings |
| `SearchRepository` | All DB queries. Injected interface for testability. Enforces `org_id` scoping |
| `ResultRanker` | Re-ranks ANN results: `final_score = 0.7 * similarity + 0.3 * recency`. Configurable weights |
| `QueryParser` | Parses + validates structured query params from HTTP request |

### API Endpoints

| Endpoint | Purpose |
|----------|---------|
| `GET /search` | Semantic (if `q` param present) or structured search |
| `GET /flags` | List all flags for an org. Filter by source, environment, tag |
| `GET /flags/:flag_key` | Flag detail — current state from `normalized_events` + paginated event history |
| `GET /flags/:flag_key/history` | Paginated audit event history. Supports date range filters |
| `GET /timeline` | Unified event timeline for an org within a date range |
| `GET /healthz` | Liveness probe |
| `GET /metrics` | Prometheus scrape |

### Constraints

- Read-only. Must never write to any table
- All queries scoped by `org_id`
- Latency targets: p99 < 2s semantic search, p99 < 500ms structured search
- Query embedding must use the same model as stored embeddings
- HNSW search: filter `org_id` (and optionally `environment_key`) before the vector scan

---

## 7. Database Schema

Two PostgreSQL 16 instances:
- **`temporal-postgres`** — Temporal's internal state DB. Managed by Temporal; do not write to it directly.
- **`app-postgres`** — All application tables below.

### `organizations`

One row per customer tenant. Primary isolation boundary. Source-agnostic — no provider-specific columns here.

```sql
CREATE TABLE organizations (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text NOT NULL,
  active      boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now()
);
```

### `integrations`

One row per provider connection per org. An org can have multiple integrations (e.g. LaunchDarkly + Unleash, or two separate LD projects). Each integration gets its own Temporal Schedule.

```sql
CREATE TYPE integration_source AS ENUM (
  'launchdarkly', 'unleash', 'flagsmith', 'github', 'pagerduty'
);

CREATE TABLE integrations (
  id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                 uuid NOT NULL REFERENCES organizations(id),
  source                 integration_source NOT NULL,
  api_key                text NOT NULL,         -- AES-256-GCM encrypted; fetched by worker at activity start
  project_key            text NOT NULL,         -- source system project identifier
  environment_key        text,                  -- null = track all environments
  poll_interval_minutes  int NOT NULL DEFAULT 60,
  backfill_days          int NOT NULL DEFAULT 30,
  enabled                boolean NOT NULL DEFAULT true,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX integrations_org_id_idx ON integrations (org_id);
-- One connection per org + source + project (prevents duplicate integrations)
CREATE UNIQUE INDEX integrations_org_source_project_idx
  ON integrations (org_id, source, project_key);
```

### `raw_events`

One row per audit log event. Written by `ExplodeAndWrite`. The only copy of raw source payloads — never deleted.

`source_event_id` is the stable ID from the source API. It is only unique within a single integration — not globally — so dedup is keyed on `(integration_id, source_event_id)`.

```sql
CREATE TABLE raw_events (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  integration_id   uuid NOT NULL REFERENCES integrations(id),
  org_id           uuid NOT NULL REFERENCES organizations(id),  -- denormalised for query efficiency
  source_event_id  text NOT NULL,                               -- stable ID from source API
  raw_json         jsonb NOT NULL,
  flag_keys        text[] NOT NULL DEFAULT '{}',                -- extracted at ingest to avoid re-parsing
  ingested_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (integration_id, source_event_id)
);

CREATE INDEX raw_events_org_id_idx   ON raw_events (org_id);
CREATE INDEX raw_events_ingested_idx ON raw_events (ingested_at);
```

### `flag_cache`

Deduplication cache for flag detail API responses. Replaces Redis. Keyed per `(integration_id, environment_key, flag_key)` — `integration_id` rather than `org_id` because flag keys are not globally unique across sources (LaunchDarkly and Unleash can both use the same key string). Environment is required to prevent production state overwriting staging.

```sql
CREATE TABLE flag_cache (
  integration_id   uuid NOT NULL REFERENCES integrations(id),
  environment_key  text NOT NULL,
  flag_key         text NOT NULL,
  data             jsonb NOT NULL,
  fetched_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (integration_id, environment_key, flag_key)
);

-- Vacuum stale entries nightly
SELECT cron.schedule(
  'vacuum-flag-cache',
  '0 3 * * *',
  $$DELETE FROM flag_cache WHERE fetched_at < now() - interval '1 hour'$$
);
```

TTL check in `CheckFlagCache`: `WHERE fetched_at > now() - interval '5 minutes'`

### `enriched_events`

Exploded model — one row per `(raw_event_id, flag_key)` pair. Written by `ExplodeAndWrite`. Upsert on PK makes re-running enrichment idempotent. Retry granularity: a failed embedding retries one flag, not the whole audit event.

```sql
CREATE TABLE enriched_events (
  raw_event_id     uuid NOT NULL REFERENCES raw_events(id),
  integration_id   uuid NOT NULL REFERENCES integrations(id),
  org_id           uuid NOT NULL REFERENCES organizations(id),  -- denormalised for query efficiency
  environment_key  text NOT NULL DEFAULT 'production',
  flag_key         text NOT NULL,
  flag_data        jsonb NOT NULL,  -- snapshot from flag_cache at time of event
  raw_event        jsonb NOT NULL,  -- copy of raw_events.raw_json
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (raw_event_id, flag_key)
);

CREATE INDEX enriched_events_org_env_idx
  ON enriched_events (org_id, environment_key);
```

### `normalized_events`

Canonical schema decoupled from the source API shape. Written by `NormalizeEvent`. Queried by the Search Service for structured search and flag history.

```sql
CREATE TABLE normalized_events (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  raw_event_id     uuid NOT NULL,
  flag_key         text NOT NULL,
  integration_id   uuid NOT NULL REFERENCES integrations(id),
  org_id           uuid NOT NULL REFERENCES organizations(id),  -- denormalised for query efficiency
  event_type       text NOT NULL,  -- flag.created, flag.updated, flag.deleted, targeting.changed
  actor_id         text,
  actor_email      text,
  flag_name        text,
  flag_kind        text,           -- boolean, multivariate, etc.
  project_key      text,
  environment_key  text NOT NULL DEFAULT 'production',
  occurred_at      timestamptz,
  metadata         jsonb NOT NULL DEFAULT '{}',  -- is_rollback, is_automated, etc.
  created_at       timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (raw_event_id, flag_key) REFERENCES enriched_events(raw_event_id, flag_key)
);

-- Covers the dashboard default filter (production, recent events)
CREATE INDEX normalized_events_org_env_occurred_idx
  ON normalized_events (org_id, environment_key, occurred_at DESC);

CREATE INDEX normalized_events_flag_key_idx
  ON normalized_events (flag_key);

CREATE INDEX normalized_events_actor_idx
  ON normalized_events (org_id, actor_email, occurred_at DESC);
```

### `event_embeddings`

Vector embeddings for semantic search. Written by `StoreEmbedding`. Separate from `normalized_events` so embeddings can be regenerated without touching the canonical event record.

```sql
CREATE TABLE event_embeddings (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  normalized_id  uuid NOT NULL REFERENCES normalized_events(id),
  org_id         uuid NOT NULL REFERENCES organizations(id),
  embedding      vector(1536) NOT NULL,   -- 1536 dims for text-embedding-3-small
  model          text NOT NULL,
  input_text     text NOT NULL,           -- text sent to embedding model, stored for debugging/re-embedding
  created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX event_embeddings_hnsw_idx
  ON event_embeddings
  USING hnsw (embedding vector_cosine_ops)
  WITH (m = 16, ef_construction = 64);
```

**HNSW build strategy:** Do not build the HNSW index during onboarding backfill. Bulk-insert all vectors first, then build the index once in a single `CREATE INDEX` pass after backfill completes. pgvector's HNSW is not incrementally updated — building during high-insert load causes write amplification. ANN search falls back to sequential scan until the index exists, which is acceptable during backfill.

### Migration Order

| File | Description |
|------|-------------|
| `001_bootstrap.sql` | Enable `uuid-ossp`, `vector`, `pg_cron` extensions |
| `002_organizations.sql` | organizations table |
| `003_integrations.sql` | integrations table + indexes (depends on organizations) |
| `004_raw_events.sql` | raw_events table + UNIQUE(integration_id, source_event_id) + indexes |
| `005_flag_cache.sql` | flag_cache with `(integration_id, environment_key, flag_key)` PK + cron vacuum |
| `006_enriched_events.sql` | enriched_events with `integration_id` + `(raw_event_id, flag_key)` PK + index |
| `007_normalized_events.sql` | normalized_events with `integration_id` + composite indexes |
| `008_event_embeddings.sql` | event_embeddings + HNSW index |

---

## 8. Shared Types

Defined in `shared/` and imported by all services.

| Type | Description |
|------|-------------|
| `EventType` | `flag.created \| flag.updated \| flag.deleted \| targeting.changed \| PRMerged \| IncidentTriggered` |
| `Source` | `LaunchDarkly \| Unleash \| Flagsmith \| GitHub \| PagerDuty` |
| `EmbedProvider` | Interface: `Embed(text string) ([]float32, error)`. Implementations: `OpenAIEmbedProvider`, `OllamaEmbedProvider` |

---

## 9. Open Questions

Do not make implementation decisions that depend on unresolved questions.

| Question | Options | Needed by |
|----------|---------|-----------|
| ~~Per-environment flag tracking~~ | **Decided: one row per flag per environment.** `environment_key` is part of `flag_cache` PK and `enriched_events`/`normalized_events` filtering dimension. Not a tenant boundary. | — |
| ~~Multi-source orgs~~ | **Decided: `integrations` table.** `organizations` is source-agnostic. Each provider connection is a row in `integrations` with its own encrypted `api_key`, `source` enum, and independent Temporal Schedule. Adding a second provider is purely additive. | — |
| Flag detail page in MVP | Include (requires per-flag paginated history) vs. defer to P0 (launch with activity feed + search only) | MVP scope decision |
| Statsig / PostHog support | Add to `integration_source` enum? Referenced in product requirements notes but not currently planned | Before second provider |
| Ollama vector dimension mismatch (768 vs 1536) | Zero-pad to 1536 (wastes storage) vs configurable column per org (schema complexity) | Before self-hosted GA |
| Weekly digest generation | Cron in Temporal (scheduled workflow) vs separate Digest Service | Phase 2 planning |
| Go DB access layer | sqlc, pgx raw, GORM | LLD session |
| Hosting / compute | Railway, Fly.io, GCP Cloud Run, self-hosted VPS | Cost analysis |
| `PublishEvent` activity downstream | Webhook, Slack notification, internal event bus | Post-MVP |

### Known Optimizations (not blocking, but tracked)

- **`flag_keys` extraction at ingest:** `FetchAuditLogPage` extracts `flag_keys` from the raw payload and stores it on `raw_events` to avoid re-parsing the JSONB in later activities.
- **Circuit breaker on source API calls:** Add circuit breaker (closed → open → half-open) on `FetchAuditLogPage` and `BatchFetchFlags` to prevent hammering a degraded upstream. Configure as Temporal activity retry policy with max attempts + backoff. Not blocking for MVP.

---

## 10. Roadmap Beyond MVP

Do not build these yet, but **do not design the MVP in a way that makes them hard to add**.

### Future Data Sources

| Source | Category | Example Questions Unlocked |
|--------|----------|---------------------------|
| GitHub | Code | Which PR introduced this flag? Time from commit → prod? |
| PagerDuty | Incidents | Was there an incident open when this flag was rolled back? |
| Datadog | Business metrics | Did error rate spike when this flag was enabled? |
| Snowflake | Data warehouse | What was conversion rate during this flag's rollout? |
| Jira / Linear | Project tracking | Which ticket drove this flag? Cycle time from ticket → launched? |

### Future Capabilities

- AI-generated summaries and natural language querying (RAG over normalised events)
- Email / Slack alerting (e.g. "a flag was rolled back in production")
- Public API for customers to query DataGill data programmatically
- Self-serve billing / plan management
- Cross-source correlation (flag change + metric anomaly on the same timeline)
- Weekly VP digest — automated summary of flag activity

---

## 11. Monorepo Structure

```
datagill-core/
├── go.work                      # Go workspace linking all modules
├── Makefile                     # proto, build, run-*, test, lint
├── buf.yaml + buf.gen.yaml      # Protobuf generation config
├── proto/                       # .proto source files (source of truth for types)
│   └── ping/v1/ping.proto
├── shared/                      # Shared Go module: generated proto types, canonical types, EmbedProvider interface
│   └── gen/ping/v1/ping.pb.go
├── services/
│   ├── worker/                  # Temporal worker — all ingestion + processing activities and workflows
│   │   ├── main.go
│   │   ├── integration_test.go
│   │   ├── go.mod
│   │   └── Dockerfile
│   └── search/                  # Search Service — port 8082
│       ├── main.go
│       ├── integration_test.go
│       ├── go.mod
│       └── Dockerfile
├── frontend/                    # Next.js app (not yet started)
└── docker-compose.yml           # Local development
```

### Docker Compose Services

| Service | Image | Purpose |
|---------|-------|---------|
| `temporal-postgres` | postgres:16 | Temporal's internal state DB (separate from app DB) |
| `temporal` | temporalio/auto-setup:1.24 | Temporal server — runs auto-migration on first start |
| `temporal-ui` | temporalio/ui:2.26 | Temporal Web UI on :8080 |
| `app-postgres` | postgres:16 | Application data — all tables in §7 |
| `worker` | your build | Temporal worker. Scale via replicas |
| `search` | your build | Search Service HTTP server on :8082 |

Each service is an independently deployable Go binary with its own `go.mod`. The `go.work` file links them together for local development. The `shared/` module is the only cross-service Go dependency.

---

*Last updated: 2026-03-16*
*Status: v0.4 — product requirements added (MVP / P0 / P1 / P2); sections renumbered*
