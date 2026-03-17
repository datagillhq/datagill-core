# CLAUDE.md — DataGill Platform: Source of Truth

> Single source of truth for architecture decisions, product requirements, and open questions.
> Every agent and every PR must trace back to this document.
> Do not implement something that contradicts this document without updating it first.
> **TBD** means the decision has not been made — do not invent one.

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

> "All 3" = LaunchDarkly, Unleash, Flagsmith. "LD" = LaunchDarkly only.

### MVP — Lights On

| Feature | Source | Notes |
|---------|--------|-------|
| Activity feed (who changed what, when) | All 3 | LD `titleVerb` is cleanest canonical description |
| Flag status (off / on / partial) | All 3 | LD direct; Unleash/Flagsmith need inference |
| Trend line (changes over time) | All 3 | Aggregate by day |
| Actor attribution (last modified by) | All 3 | Normalize to `actor_email` |
| Search by flag name / time range | All 3 | LD supports full-text; others: time range only |

### P0 — Root Cause Analysis

| Feature | Source | Notes |
|---------|--------|-------|
| "What changed in the last X hours?" scoped to prod | All 3 | Filter by environment + time window |
| Automated vs. human change detection | LD | `token.serviceToken` field distinguishes API vs. human |
| Rollback detection | All 3 | Flag toggled back within short window |
| Per-flag timeline view | All 3 | Paginated history per flag, newest first |

### P1 — Developer Velocity

| Feature | Source | Notes |
|---------|--------|-------|
| Time from flag creation → on in prod | All 3 | Requires `first_seen_at` per flag |
| Stale flag list (created, never launched) | All 3 | No "enabled" event past X days |
| Dead flag list (launched 60+ days, no changes) | All 3 | Compare `last_event_at` |
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
| Natural language search | LLM over stored embeddings — do not rely on source `q` param alone |
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
| Embeddings | OpenAI text-embedding-3-small / Ollama nomic-embed-text | Swappable via `EmbedProvider` interface |
| Orchestration | Self-hosted Temporal | Replaces queues. Durable state, retry, scheduling in one primitive |
| Flag cache | PostgreSQL `flag_cache` table | Replaces Redis; TTL enforced inline, vacuumed nightly by pg_cron |
| Migrations | golang-migrate | Run at startup or as a separate job |
| Metrics | Prometheus + Grafana | Scrape endpoint at `/metrics` on all services |
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

```
  LaunchDarkly / Unleash / Flagsmith
         │  poll every 5 min per integration
         ▼
  ┌─────────────────────────────────────────┐
  │  Temporal (self-hosted)                 │
  │                                         │
  │  IntegrationIngestWorkflow              │
  │    └─ EnrichBatchWorkflow (per page)    │
  │         ├─ FetchAuditLogPage            │
  │         ├─ CheckFlagCache               │
  │         ├─ BatchFetchFlags              │
  │         ├─ WriteFlagCache               │
  │         └─ ExplodeAndWrite              │
  │              └─ NormalizeAndEmbedWorkflow│
  │                   ├─ NormalizeEvent     │
  │                   ├─ GenerateEmbedding  │
  │                   └─ StoreEmbedding     │
  └─────────────────────────────────────────┘
         │ writes
         ▼
  ┌──────────────────────────────┐
  │  PostgreSQL (app DB)         │
  │  organizations · integrations│
  │  raw_events · flag_cache     │
  │  enriched_events             │
  │  normalized_events           │
  │  event_embeddings            │
  └──────────────────────────────┘
         │
         ▼
  Search Service :8082 → Frontend
```

### Temporal Schedule (per integration)

| Property | Value |
|----------|-------|
| Frequency | Every 5 min |
| Overlap | `ScheduleOverlapPolicy.SKIP` — in-progress runs are not interrupted |
| Jitter | Up to 30s at startup to spread load |
| Cursor | Stored in workflow durable state (`before` timestamp, Unix ms) — no DB column |
| History | `Continue-As-New` every 100 pages |
| Lifecycle | Created on integration enable; paused/deleted on `enabled = false` |

### Pagination (LaunchDarkly)

LD audit log returns max **20 entries per page**. No cursor token — walk pages by passing the earliest `date` in the current page as `before` on the next request. `EnrichBatchWorkflow` is dispatched **fire-and-forget** per page (fetching is sequential; enrichment is parallel). Cap concurrent `EnrichBatchWorkflow`s during backfill to avoid fan-out.

---

## 4. Pipeline Flow

```
1. SCHEDULE
   IntegrationIngestWorkflow fires every 5 min per integration.
   Cursor = before-timestamp in durable state.

2. FETCH PAGE  [FetchAuditLogPage]
   GET /auditlog?before=<cursor>&limit=20
   Spawn EnrichBatchWorkflow (fire-and-forget) for this page.
   Advance cursor to earliest date in page. Repeat until empty page.
   Continue-As-New after 100 pages.

3. ENRICH BATCH  [per page]
   CheckFlagCache  — lookup (integration_id, env, flag_key), TTL 5 min
   BatchFetchFlags — fetch from source API for cache misses
   WriteFlagCache  — upsert cache rows
   ExplodeAndWrite — write raw_events (dedup: UNIQUE integration_id+source_event_id)
                     explode to enriched_events (one row per event × flag_key)
                     spawn NormalizeAndEmbedWorkflow per row (fire-and-forget)

4. NORMALIZE & EMBED  [per enriched_events row]
   NormalizeEvent     — map to canonical schema; detect rollback + automated changes
   GenerateEmbedding  — call EmbedProvider → vector(1536)
   StoreEmbedding     — write to event_embeddings

5. SERVE
   Semantic: embed query → ANN search → re-rank by recency → top 20
   Structured: filters → parameterized SQL over normalized_events
```

### Invariants

- **Raw data is never deleted.** `raw_events` is the only copy of source payloads. Reprocess from here on failure.
- **All writes are idempotent.** Upsert on stable keys throughout; reprocessing any event is always safe.
- **Dedup at the DB layer.** `UNIQUE(integration_id, source_event_id)` on `raw_events`.
- **Every query filters by `org_id` first.** No cross-tenant leakage. Ever.
- **`flag_cache` scoped per integration.** Flag keys are not unique across providers.
- **Activities are the unit of retry.** Never retry in application code — let Temporal handle it.

---

## 5. Search Service

**Endpoints**

| Endpoint | Purpose |
|----------|---------|
| `GET /search` | Semantic (with `q`) or structured search |
| `GET /flags` | List all flags. Filter by source, environment |
| `GET /flags/:flag_key` | Flag detail + paginated event history |
| `GET /flags/:flag_key/history` | Event history with date range filters |
| `GET /timeline` | Unified event timeline for an org |
| `GET /healthz` | Liveness |
| `GET /metrics` | Prometheus scrape |

**Constraints**
- Read-only. Never writes to any table.
- All queries scoped by `org_id`.
- Latency targets: p99 < 2s semantic, p99 < 500ms structured.
- Query embedding must use the same model as stored embeddings.
- HNSW: filter `org_id` (+ optionally `environment_key`) before the vector scan.

---

## 6. Database Schema

Two PostgreSQL 16 instances: `temporal-postgres` (Temporal internals — do not touch) and `app-postgres` (all tables below).

### `organizations`
One row per customer tenant. No provider-specific columns.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| name | text | |
| active | bool | Default true |
| created_at | timestamptz | |

### `integrations`
One row per provider connection per org. Each row gets its own Temporal Schedule.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| org_id | uuid | FK → organizations |
| source | enum | `launchdarkly \| unleash \| flagsmith \| github \| pagerduty` |
| api_key | text | AES-256-GCM encrypted |
| project_key | text | Source system project identifier |
| environment_key | text | Null = track all environments |
| poll_interval_minutes | int | Default 60 |
| backfill_days | int | Default 30 |
| enabled | bool | Default true |
| created_at / updated_at | timestamptz | |

Key indexes: `(org_id)`. `UNIQUE(org_id, source, project_key)`.

### `raw_events`
One row per audit log event. Only copy of raw source payloads — never deleted.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| integration_id | uuid | FK → integrations |
| org_id | uuid | Denormalized for query efficiency |
| source_event_id | text | Stable ID from source API |
| raw_json | jsonb | Full raw payload |
| flag_keys | text[] | Extracted at ingest to avoid re-parsing |
| ingested_at | timestamptz | |

Key indexes: `UNIQUE(integration_id, source_event_id)`. `(org_id)`. `(ingested_at)`.

### `flag_cache`
Short-lived cache of flag detail API responses. Replaces Redis. TTL 5 min (inline check); vacuumed nightly by pg_cron.

| Column | Type | Notes |
|--------|------|-------|
| integration_id | uuid | PK component |
| environment_key | text | PK component — required, prevents prod overwriting staging |
| flag_key | text | PK component |
| data | jsonb | Full flag state snapshot |
| fetched_at | timestamptz | TTL anchor |

PK: `(integration_id, environment_key, flag_key)`.

### `enriched_events`
Exploded model — one row per `(raw_event_id, flag_key)`. Upsert on PK = idempotent re-runs. Retry granularity: one failed embedding = retry one flag, not the whole audit event.

| Column | Type | Notes |
|--------|------|-------|
| raw_event_id | uuid | PK component, FK → raw_events |
| flag_key | text | PK component |
| integration_id | uuid | FK → integrations |
| org_id | uuid | Denormalized |
| environment_key | text | Default 'production' |
| flag_data | jsonb | Flag snapshot from cache at event time |
| raw_event | jsonb | Copy of raw_events.raw_json |
| created_at | timestamptz | |

PK: `(raw_event_id, flag_key)`. Index: `(org_id, environment_key)`.

### `normalized_events`
Canonical schema, decoupled from source API shape. Queried by Search Service.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| raw_event_id | uuid | FK component → enriched_events |
| flag_key | text | FK component → enriched_events |
| integration_id | uuid | FK → integrations |
| org_id | uuid | Denormalized |
| event_type | text | `flag.created \| flag.updated \| flag.deleted \| targeting.changed` |
| actor_id / actor_email | text | Normalized from source |
| flag_name / flag_kind | text | |
| project_key | text | |
| environment_key | text | Default 'production' |
| occurred_at | timestamptz | Source timestamp — never ingestion time |
| metadata | jsonb | `is_rollback`, `is_automated`, etc. |
| created_at | timestamptz | |

Key indexes: `(org_id, environment_key, occurred_at DESC)`. `(flag_key)`. `(org_id, actor_email, occurred_at DESC)`.

### `event_embeddings`
Vector embeddings for semantic search. Separate from `normalized_events` so embeddings can be regenerated independently.

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK |
| normalized_id | uuid | FK → normalized_events |
| org_id | uuid | |
| embedding | vector(1536) | text-embedding-3-small dimensions |
| model | text | Model name for re-embedding compatibility |
| input_text | text | Text sent to model — stored for debugging |
| created_at | timestamptz | |

Key indexes: `HNSW(embedding, cosine)` with `m=16, ef_construction=64`.

> **HNSW backfill note:** Build the index in one `CREATE INDEX` pass *after* bulk-inserting all vectors. Building incrementally during high-insert load causes write amplification.

### Migration Order

| File | Creates |
|------|---------|
| `001_bootstrap.sql` | Extensions: `uuid-ossp`, `vector`, `pg_cron` |
| `002_organizations.sql` | organizations |
| `003_integrations.sql` | integrations + indexes |
| `004_raw_events.sql` | raw_events + dedup index |
| `005_flag_cache.sql` | flag_cache + cron vacuum |
| `006_enriched_events.sql` | enriched_events |
| `007_normalized_events.sql` | normalized_events + indexes |
| `008_event_embeddings.sql` | event_embeddings + HNSW index |

---

## 7. Open Questions

> Do not make implementation decisions that depend on unresolved questions.

| Question | Options | Needed by |
|----------|---------|-----------|
| Flag detail page in MVP | Include now vs. defer to P0 | MVP scope call |
| Statsig / PostHog support | Add to `integration_source` enum? | Before second provider |
| `feature_flags` table | Add a per-flag entity table for current state + `first_seen_at` / `last_event_at` (needed for stale/dead flag lists) vs. derive on query | Before P1 |
| `EnrichBatchWorkflow` concurrency cap | Max in-flight during backfill — value TBD | Before backfill impl |
| Ollama dimension mismatch (768 vs 1536) | Zero-pad vs. configurable column per org | Before self-hosted GA |
| Weekly digest | Scheduled Temporal workflow vs. separate service | Phase 2 |
| Go DB access layer | sqlc, pgx raw, GORM | LLD session |
| Hosting / compute | Railway, Fly.io, GCP Cloud Run, VPS | Cost analysis |
| `PublishEvent` downstream | Webhook, Slack, internal bus | Post-MVP |

---

## 8. Roadmap

Do not build these yet. **Do not design the MVP in a way that makes them hard to add.**

| Source | Category | Questions Unlocked |
|--------|----------|--------------------|
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
├── Makefile
├── shared/           # shared Go types, EmbedProvider interface
├── services/
│   ├── worker/       # Temporal worker (port: none — connects to Temporal server)
│   └── search/       # Search Service (port: 8082)
└── frontend/         # Next.js (not yet started)
```

**Docker Compose services:** `temporal-postgres`, `temporal`, `temporal-ui` (:8080), `app-postgres`, `worker`, `search` (:8082).

---

*Last updated: 2026-03-17 — v0.5: trimmed for reviewability*
