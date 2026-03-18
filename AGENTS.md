# AGENTS.md — DataGill Platform: Source of Truth

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

## 1. Technology Decisions

### Decided

| Layer | Choice | Rationale |
|-------|--------|-----------|
| Frontend | Next.js (App Router) + shadcn/ui | Best-in-class React SSR, shadcn gives us accessible, unstyled components we own |
| Backend language | **Go** | Goroutines are a natural fit for the ETL workload — concurrent provider polling, parallel normalization jobs, low memory per goroutine |
| Go HTTP framework | **Chi** | Lightweight, idiomatic, stdlib-compatible. Easy to add middleware and route groups as endpoints grow |
| Database | **PostgreSQL 16 + pgvector** | ACID guarantees, JSONB for raw payload flexibility, pgvector for semantic search embeddings. One DB for all services |
| Auth (platform) | **Clerk** | Handles user login, org management, and JWT issuance. Language-agnostic — Go validates Clerk JWTs via JWKS endpoint |
| Embedding model | **OpenAI text-embedding-3-small** (cloud) / **Ollama nomic-embed-text** (self-hosted) | Swappable via EmbedProvider interface — same interface, different implementation |
| Queue | **BullMQ + Redis** (everywhere) | Single implementation for cloud and self-hosted. Cloud uses ElastiCache, self-hosted uses Docker Redis. Three priority tiers (HIGH / NORMAL / LOW) |
| Metrics | **Prometheus + Grafana Cloud** (cloud) / **Prometheus + Grafana** (self-hosted) | Standard scrape endpoint at `/metrics` on all services |
| Secret management | **AWS Secrets Manager** (cloud) / **env vars + Docker secrets** (self-hosted) | |
| DB migrations | **golang-migrate** | Applied at service startup or as a separate migration job |
| Search Service auth | **Clerk JWT** | Frontend passes Clerk JWT; Search Service validates via JWKS. No per-resource authorization yet — everyone is admin. Permissions TBD post-MVP |
| Organisation creation | **Clerk webhook** | An `organisations` row is created on business onboarding when the Clerk org is provisioned. Clerk org ID is the primary external reference |
| Provider auth (MVP) | **API key** | Customer generates read-only API key in their provider, pastes into DataGill. Encrypted at rest with AES-256-GCM |
| Search backend | **PostgreSQL + pgvector** | HNSW index for ANN semantic search. Structured search via parameterized SQL |
| Webhooks (v1) | **None — poll only** | No inbound public endpoints in v1. Customers provide an API key; we poll on a schedule |

### Not Decided Yet (TBD)

| Decision | Options Being Considered | Blocking On |
|----------|--------------------------|-------------|
| Go DB access layer | sqlc, pgx raw, GORM | LLD session |
| Hosting / compute | Railway, Fly.io, GCP Cloud Run, self-hosted VPS | Cost analysis |
| Frontend state / data fetching | TanStack Query, SWR, RSC-only | LLD session |
| Provider auth (V2) | OAuth flow so customers don't manage keys manually | Post-MVP |

---

## 2. Architecture Overview

The platform is three independent Go services connected by a priority queue and a shared PostgreSQL database.

```
  Customer tools (LaunchDarkly, Unleash, GitHub, PagerDuty)
         |
         |  Poll-based (audit log API + detail fetch)
         v
  +------------------------------------------+
  |         Ingestion Service  :8080          |
  |  PollScheduler (fixed delay + jitter)     |
  |  PollLockManager (multi-instance safe)    |
  |  AuditLogFetcher + DetailFetcher          |
  |  RawEventStore → ingestion_raw_events     |  ← only raw storage in system
  |  Normaliser + PriorityClassifier          |
  |  DeduplicatorService                      |
  |  QueuePublisher                           |
  +------------------------------------------+
         |                |                |
      HIGH queue      NORMAL queue      LOW queue
      (enterprise)    (standard)        (backfill)
         |                |                |
         +----------------+----------------+
                          |
                          v
  +------------------------------------------+
  |       Processing Service  :8081           |
  |  QueueConsumer (weighted scheduler)       |
  |                                           |
  |  FlagOrchestrator                         |
  |    FlagInitPhase                          |
  |    EmbedPhase                             |
  |    FlagFinalizationPhase                  |
  |                                           |
  |  FlagEventOrchestrator                    |
  |    FlagEventInitPhase                     |
  |    FlagEventFinalizationPhase             |
  +------------------------------------------+
         |                    |
    feature_flags       feature_flag_events
    ops_locks            (normalised only,
    (PostgreSQL)          no raw payload)
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

### Queue Priority Model

| Tier | Who uses it | Scheduler weight |
|------|-------------|-----------------|
| HIGH | Enterprise org events, incident events | 10 |
| NORMAL | Standard polling events | 3 |
| LOW | Onboarding backfill, historical imports | 1 |

**Scheduler algorithm — counter-based round-robin:**
Each round is 14 slots (10+3+1). Fill them in order: 10 from HIGH, 3 from NORMAL, 1 from LOW.
If a queue is empty on its slot, skip it — do not steal the slot for another tier.
Starvation guard: if LOW hasn't been drained in >1000 rounds, force one LOW poll regardless.
This is intentionally simple and easy to swap for a token bucket if needed.

### Service Responsibilities

| Service | Port | Owns | Does NOT own |
|---------|------|------|--------------|
| Ingestion | 8080 | Polling, raw storage, normalisation, queue publishing | Enrichment, embeddings, serving queries |
| Processing | 8081 | Consuming queue, enrichment pipeline, embeddings, writing to DB | HTTP query serving, raw storage |
| Search | 8082 | Serving semantic + structured search queries | Writing to any table |
| Frontend | — | UI rendering, data display, user interaction | Business logic, direct DB access |

---

## 3. ETL Pipeline — The Flow

```
Step 1: EXTRACT
  └─ PollScheduler triggers per integration (fixed delay + startup jitter)
     → AuditLogFetcher paginates audit log API since cursor, one page at a time
     → CursorStore.Update(latest_timestamp_on_page) after each page — not at end of poll
        This means a crash mid-poll resumes from the last completed page, not the beginning
     → DetailFetcher fetches full current state — deduplicated per unique flag_key per poll cycle
        (50 audit entries across 10 flags = 10 detail fetches, not 50)

Step 2: DEDUPLICATE
  └─ DeduplicatorService checks (source + source_event_id + org_id) in DB
     Already seen? Skip. Not seen? Continue.

Step 3: RAW STORAGE
  └─ RawEventStore writes audit_log_payload to ingestion_raw_events (JSONB)
     published_at = null until queue publish succeeds

Step 4: NORMALISE
  └─ Normaliser maps source-specific payload → canonical RawEvent struct
     Produces two events per audit entry:
       RawFlagEvent        → feature-flag-{priority}-queue
       RawFlagAuditEvent   → feature-flag-event-{priority}-queue

Step 5: ENQUEUE
  └─ PriorityClassifier assigns HIGH / NORMAL / LOW based on org tier + event type
     QueuePublisher publishes to correct queue
     RawEventStore.MarkPublished sets published_at = now()

Step 6: PROCESS — Pipeline 1 (Feature Flag Entity)
  └─ FlagInitPhase: acquire ops_lock, UPSERT feature_flags, set ShouldEmbed
     EmbedPhase: call EmbedProvider if ShouldEmbed=true → vector
     FlagFinalizationPhase: write embedding, update last_event_at, release lock

Step 7: PROCESS — Pipeline 2 (Feature Flag Audit Event)
  └─ FlagEventInitPhase: INSERT feature_flag_events (normalised fields only)
     FlagEventFinalizationPhase: UPDATE feature_flags.last_event_id

Step 8: SERVE
  └─ Search Service receives query from frontend
     Semantic: embed query → ANN search → re-rank by recency → return top 20
     Structured: parse filters → parameterized SQL → return results
```

### Critical Invariants (never violate these)

- **Raw data is never discarded.** `ingestion_raw_events` is the only copy of raw payloads in the system. Never delete. If processing fails, reprocess from this table.
- **Deduplication at the DB layer.** `UNIQUE(integration_id, source_event_id)` on `ingestion_raw_events` — safe to receive the same event twice.
- **Every query filters by `org_id` first.** No cross-tenant data leakage. Ever.
- **Processing Service stores normalised data only.** No raw payloads in `feature_flag_events`. Raw data lives exclusively in the Ingestion Service's `ingestion_raw_events`.
- **Webhook handlers return immediately (future).** Never do synchronous processing inside a webhook handler. Store raw, dispatch job, return 200.
- **All writes must be idempotent.** Reprocessing any event must be safe.
- **Middle phases (EmbedPhase) must not touch the database.** Only Init and Finalization phases interact with DB.
- **ops_lock must be acquired before any write to a shared entity in Pipeline 1.** Pipeline 2 is append-only (`feature_flag_events`) plus a single FK update — it does not require a lock. Lock key is `resource_key text`, not a UUID, so it can be acquired before the entity row exists.

---

## 4. Ingestion Service

### Components

| Component | Responsibility |
|-----------|---------------|
| `IngestionService` | Entry point. Wires all components, manages scheduler lifecycle, handles shutdown |
| `PollScheduler` | Fixed delay per integration goroutine. Random startup jitter to prevent thundering herd |
| `PollLockManager` | Acquires/releases rows in `poll_locks`. Prevents concurrent polls across instances |
| `SourcePoller` | Interface. One implementation per source: `LaunchDarklyPoller`, `UnleashPoller`, etc |
| `AuditLogFetcher` | Paginates source audit log API. Returns ordered `[]AuditLogEntry` |
| `DetailFetcher` | Given a resource key, fetches full current state from source API |
| `RawEventStore` | Writes to `ingestion_raw_events`. Sets `published_at` after successful queue publish |
| `Normaliser` | Pure function. Maps source payload → canonical `RawEvent`. One per source |
| `PriorityClassifier` | Assigns HIGH / NORMAL / LOW based on org tier and event type |
| `DeduplicatorService` | Checks `(source, source_event_id, org_id)` in DB before processing |
| `QueuePublisher` | Interface. `SQSPublisher` and `BullMQPublisher` implementations |
| `CursorStore` | Reads/writes `last_seen_at` per integration to `integration_cursors` |
| `OrgConfig` | Org settings including queue tier |

### Constraints

- Back off on 429, respect `Retry-After` headers from source APIs
- Stateless between poll cycles — all cursor state in DB
- Must not call Processing Service directly — all communication via queue
- Poll scheduling uses **fixed delay** (not fixed interval) — next poll starts only after previous completes
- Multiple instances safe — `poll_locks` table prevents concurrent polls for the same integration
- Onboarding backfill is the same code path as live polling — cursor set to 30 days ago, events go to LOW queue

---

## 5. Processing Service

### Framework Abstractions

| Abstraction | Responsibility |
|-------------|---------------|
| `ProcessingService` | Entry point. Wires orchestrators, HTTP server, metrics. Graceful shutdown |
| `QueueConsumer` | Reads `RawEvent` off queue. Manages visibility timeout extension. ACKs on success |
| `Orchestrator` | Owns ordered list of `PhaseRunner`s. Wires inter-phase channels |
| `PhaseRunner` | Wraps one `Phase`. Owns configurable worker goroutine pool. Restarts crashed workers |
| `Phase` (interface) | `Process(EventContext) → (EventContext, error)`. Pure logic. No DB, no queues |
| `EventContext` | Immutable data carrier flowing through all phases. Each phase adds its result field |
| `EventStore` (interface) | DB operations. Used only by Init and Finalization phases. Injected for testability |
| `EmbedProvider` (interface) | `OpenAIEmbedProvider` and `OllamaEmbedProvider`. Swapped via config |
| `MetricsCollector` (interface) | Per-phase throughput, latency, error rate. `PrometheusMetricsCollector` implementation |
| `OpsLockManager` | Acquires/releases rows in `ops_locks`. Used by Init and Finalization phases |

### Pipeline 1 — Feature Flag Entity

```
feature-flag-{priority}-queue
  → FlagInitPhase    — acquire ops_lock, UPSERT feature_flags, set ShouldEmbed
  → EmbedPhase       — call EmbedProvider if ShouldEmbed=true (no DB access)
  → FlagFinalizationPhase — write embedding, update metadata, release ops_lock
```

### Pipeline 2 — Feature Flag Audit Event

Historical record only. No embedding. No ops_lock needed — this pipeline only appends to
`feature_flag_events` and does a single FK update on `feature_flags`.

To handle the ordering race with Pipeline 1 (flag entity row may not exist yet):
`FlagEventInitPhase` does `INSERT INTO feature_flags ... ON CONFLICT DO NOTHING` before
inserting the event row. This ensures the parent row exists without blocking on Pipeline 1.

```
feature-flag-event-{priority}-queue
  → FlagEventInitPhase        — INSERT INTO feature_flags ON CONFLICT DO NOTHING (ensure parent exists)
                                 INSERT feature_flag_events
  → FlagEventFinalizationPhase — UPDATE feature_flags.last_event_id = this event id
```

### HTTP Endpoints (internal only)

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz` | Liveness — 200 when PhaseRunners running and DB reachable |
| `GET /readyz` | Readiness — 200 when QueueConsumer connected |
| `GET /metrics` | Prometheus scrape |
| `POST /flags/:flag_key/reprocess` | Re-queue all events for a flag key |
| `GET /events/:id/status` | Check processing status of a specific event |
| `POST /pipeline/pause` | Pause queue consumption |
| `POST /pipeline/resume` | Resume after pause |

### Constraints

- Must never write to a table without first acquiring the relevant `ops_lock`
- Middle phases must not interact with the database
- Failed events must not be retried by application code — infrastructure (SQS/BullMQ DLQ) handles retry

---

## 6. Search Service

### Components

| Component | Responsibility |
|-----------|---------------|
| `SearchService` | Entry point. HTTP server, routing, middleware, graceful shutdown |
| `SemanticSearchHandler` | Natural language queries. Embeds query, runs ANN search, re-ranks, returns results |
| `StructuredQueryHandler` | Filtered queries by source, date range, actor, environment. No embedding |
| `EmbedProvider` | Same interface as Processing Service. Must use same model as stored embeddings |
| `SearchRepository` | All DB queries. Injected interface for testability. Enforces `org_id` scoping |
| `ResultRanker` | Re-ranks ANN results: `final_score = 0.7 * similarity + 0.3 * recency`. Configurable weights |
| `QueryParser` | Parses + validates structured query params from HTTP request |

### Bootstrap Decisions (current implementation)

- Search Service is the only backend service paired with the frontend in the current local-dev bootstrap
- Search Service does **not** use Temporal and has no dependency on the other services for local UI work
- Until the Go DB access layer is decided, the service uses an in-memory `SearchRepository` with demo records so UI development can proceed without PostgreSQL
- HTTP routes are exposed both as basic probes (`/healthz`, `/readyz`) and versioned API endpoints under `/api/v1`
- Org scoping is still enforced at the handler boundary; local bootstrap uses `X-Org-Id` and defaults to `local-dev` when absent

### API Endpoints

| Endpoint | Purpose |
|----------|---------|
| `GET /api/v1/search` | Bootstrap search endpoint for UI integration. Returns demo-backed semantic/keyword results for one org |
| `GET /api/v1/flags` | Bootstrap flag listing endpoint for UI integration |
| `GET /flags/:flag_key` | Flag detail — current `enrichment_data` + paginated event history |
| `GET /flags/:flag_key/history` | Paginated audit event history. Supports date range filters |
| `GET /timeline` | Unified event timeline for an org within a date range |
| `GET /healthz` | Liveness probe |
| `GET /readyz` | Readiness probe |
| `GET /metrics` | Prometheus scrape |

### Constraints

- Read-only. Must never write to any table
- All queries scoped by `org_id`
- Latency targets: p99 < 2s semantic search, p99 < 500ms structured search
- Query embedding must use the same model as stored embeddings

---

## 7. Database Schema

### Ingestion Service Tables

**`integrations`** — One row per provider connection per org.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| org_id | uuid | FK → organisations |
| source | enum | `launchdarkly \| unleash \| flagsmith \| github \| pagerduty` |
| credentials | jsonb | AES-256-GCM encrypted |
| project_key | string | Source system project identifier |
| environment_key | string | Which environment to track. Null = all |
| poll_interval_minutes | int | Default 60 |
| backfill_days | int | Default 30 |
| enabled | bool | Can be paused without deleting |
| created_at / updated_at | timestamp | |

**`integration_cursors`** — Poll cursor per integration.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| integration_id | uuid | FK → integrations |
| org_id | uuid | Denormalised for query efficiency |
| last_seen_at | timestamp | Timestamp of last successfully processed entry |
| last_fetched_at | timestamp | When the last poll ran |
| last_entry_count | int | Entries found in last poll |
| updated_at | timestamp | |

**`poll_locks`** — Prevents concurrent polls across multiple Ingestion Service instances.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| integration_id | uuid | FK → integrations. UNIQUE |
| org_id | uuid | |
| locked_by | string | Instance ID (hostname + PID) |
| locked_at | timestamp | |
| expires_at | timestamp | Default TTL 15 min. Stale locks auto-taken-over |
| created_at | timestamp | |

Lock acquisition: `UPDATE poll_locks SET locked_by=... WHERE integration_id=$id AND (locked_by IS NULL OR expires_at < now()) RETURNING id`

**`ingestion_raw_events`** — The only copy of raw source payloads in the system. Never deleted.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| integration_id | uuid | FK → integrations |
| org_id | uuid | |
| source_event_id | string | Stable ID from source. Used for dedup |
| audit_log_payload | jsonb | Raw audit log entry + full-state detail fetch response |
| processing_status | enum | `pending \| queued \| processed \| failed` — `pending` on insert, `queued` after publish, `processed` after Pipeline finalization, `failed` after DLQ |
| published_at | timestamp | Null until QueuePublisher.Publish succeeds |
| fetched_at | timestamp | When fetched from source API |

Indexes: `UNIQUE(integration_id, source_event_id)`. `(org_id, fetched_at DESC)`. `(processing_status) WHERE processing_status IN ('pending', 'failed')` for reprocessing queries.

### Processing Service Tables

**`feature_flags`** — Current enriched state per flag per org. Upserted on every pipeline run.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| org_id | uuid | FK → organisations |
| flag_key | string | Stable identifier from source |
| environment | string | Environment name from source (e.g. `production`, `staging`). One row per flag per environment |
| source | enum | `launchdarkly \| unleash \| flagsmith` |
| enrichment_data | jsonb | Current state snapshot. Replaced on `full_snapshot`. On `diff`: application-level merge required — PostgreSQL's `||` operator replaces keys but does NOT handle deletions. The merge function must explicitly remove keys that appear as deleted in the diff, otherwise stale data accumulates silently. |
| data_completeness | enum | `partial` (diffs only) \| `full` (at least one full snapshot) |
| embedding | vector(1536) | Null until first embed run |
| embedding_text | text | Text sent to embedding model. Stored for debugging |
| last_embedded_at | timestamp | |
| first_seen_at | timestamp | |
| last_event_at | timestamp | `occurred_at` of most recent processed event |
| last_event_id | uuid | FK → feature_flag_events |
| pipeline_version | string | For targeted backfills |
| created_at / updated_at | timestamp | |

Indexes: `UNIQUE(org_id, flag_key, source, environment)`. `HNSW(embedding) WHERE embedding IS NOT NULL`. `(org_id, last_event_at DESC)`. `GIN(enrichment_data)`.

**HNSW build strategy:** Do not build the HNSW index during onboarding backfill. Bulk-insert all vectors first, then build the index once in a single `CREATE INDEX` pass after backfill completes. pgvector's HNSW is not incrementally updated — building during high-insert load causes write amplification. ANN search falls back to sequential scan until the index exists, which is acceptable during backfill.

**`feature_flag_events`** — Normalised audit log. Append-only. No raw payloads.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| org_id | uuid | FK → organisations |
| flag_key | string | Denormalised for query performance |
| source | enum | |
| source_event_id | string | For dedup and correlation back to `ingestion_raw_events` |
| change_type | enum | `full_snapshot \| diff` |
| changed_fields | string[] | Null if full_snapshot |
| changed_by | string | Actor from source audit log |
| occurred_at | timestamp | Source system timestamp. Never ingestion time |
| received_at | timestamp | When ingestion service received this entry |
| processed_at | timestamp | When finalization phase completed |

Indexes: `UNIQUE(org_id, source, source_event_id)`. `(org_id, flag_key, occurred_at DESC)`. `(org_id, occurred_at DESC)`. `(org_id, changed_by, occurred_at DESC)`.

**`ops_locks`** — Soft lock preventing concurrent processing of the same entity.

`resource_key` is a string (not a UUID) so it can hold any stable identifier — a `flag_key`,
a GitHub PR number, an incident ID — without needing to look up the entity's UUID first.
The lock is acquired on the natural key before the entity row is created.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| resource_type | string | `feature_flag \| code_event \| incident` — extensible as new pipelines are added |
| resource_key | text | Stable natural key of the entity being locked (e.g. flag_key, pr_number) |
| org_id | uuid | |
| locked_by | string | Processing service instance ID |
| locked_at | timestamp | |
| expires_at | timestamp | Default TTL 10 min. Stale locks auto-taken-over |
| pipeline_version | string | For observability |
| created_at | timestamp | |

Indexes: `UNIQUE(resource_type, org_id, resource_key)`. `(expires_at)`.

### Shared Tables

**`organisations`** — One row per customer tenant. Primary isolation boundary.

---

## 8. Shared Types

Defined in `shared/` and imported by all services.

| Type | Description |
|------|-------------|
| `RawEvent` | Normalised event from Ingestion → Processing. Fields: `source`, `event_type`, `org_id`, `occurred_at`, `raw_payload`, `change_type`, `changed_fields`, `priority` |
| `EventType` | `FlagToggled \| FlagCreated \| FlagDeleted \| FlagArchived \| FlagUpdated \| PRMerged \| IncidentTriggered` |
| `Source` | `LaunchDarkly \| Unleash \| Flagsmith \| GitHub \| PagerDuty` |
| `Priority` | `High \| Normal \| Low` |
| `ChangeType` | `FullSnapshot \| Diff` |

---

## 9. Open Questions

Do not make implementation decisions that depend on unresolved questions.

| Question | Options | Needed by |
|----------|---------|-----------|
| ~~Per-environment flag tracking~~ | **Decided: one row per flag per environment.** `UNIQUE(org_id, flag_key, source, environment)`. Accurate state per environment at the cost of multiplying rows. `environment` is a required field on `feature_flags`. | — |
| Inter-phase communication | In-process Go channels (simpler, lower latency, single instance only) vs Redis pub/sub (horizontal scale, higher latency) | Before first scale event |
| Ollama vector dimension mismatch (768 vs 1536) | Zero-pad to 1536 (wastes storage) vs configurable column per org (schema complexity) | Before self-hosted GA |
| Weekly digest generation | Cron in Processing Service (simpler) vs separate Digest Service (independent scaling) | Phase 2 planning |
| Go DB access layer | sqlc, pgx raw, GORM | LLD session |
| Hosting / compute | Railway, Fly.io, GCP Cloud Run, self-hosted VPS | Cost analysis |

### Known Optimizations (not blocking, but tracked)

- **DetailFetcher deduplication:** Within a single poll cycle, deduplicate detail fetches by `flag_key`. 50 audit entries across 10 flags should produce 10 detail fetches, not 50. Implement as a `map[flag_key]→detail` cache scoped to the poll cycle.
- **Circuit breaker on source API calls:** Add circuit breaker (closed → open → half-open) on `AuditLogFetcher` and `DetailFetcher` to prevent hammering a degraded upstream. Not blocking for MVP.

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
├── shared/                      # Shared Go module: generated proto types, canonical types
│   └── gen/ping/v1/ping.pb.go
├── services/
│   ├── ingestion/               # Ingestion Service — port 8080
│   │   ├── main.go
│   │   ├── integration_test.go
│   │   ├── go.mod
│   │   └── Dockerfile
│   ├── processing/              # Processing Service — port 8081
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

Each service is an independently deployable Go binary with its own `go.mod`. The `go.work` file links them together for local development. The `shared/` module is the only cross-service Go dependency.

---

*Last updated: 2026-03-13*
*Status: v0.1 — services scaffolded, design reviewed, implementation pending LLD session*
