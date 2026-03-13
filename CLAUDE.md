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
| Feature flags (LaunchDarkly, PostHog, Statsig) | What changed before the incident? How long does it take us to ship a flag? |
| Business metrics (e.g. Datadog) | Did this flag change correlate with a latency spike? |
| Data warehouse (e.g. Snowflake) | What did our conversion rate look like when this feature launched? |
| Code (e.g. GitHub) | Which PR shipped this flag? How long from commit to flag enabled in production? |

**MVP = feature flag providers only.** The architecture must be designed from day one to accommodate additional data source categories without rearchitecting the pipeline.

The system is an **ETL pipeline** with a search/query layer on top. Each new data source is a new integration — the pipeline, storage, and query layers stay the same.

---

## 1. Technology Decisions

### Decided

| Layer | Choice | Rationale |
|-------|--------|-----------|
| Frontend | Next.js (App Router) + shadcn/ui | Best-in-class React SSR, shadcn gives us accessible, unstyled components we own |
| Backend language | **Go** | Goroutines are a natural fit for the ETL workload — concurrent provider polling, parallel normalization jobs, low memory per goroutine. Single compiled binary makes self-hosting simpler. |
| Async workers | **Go goroutines** | Replace Node worker threads. The normalize and enrich pipeline runs as concurrent goroutines, not a separate process — simpler operationally, better performance |
| Database | **PostgreSQL** | ACID guarantees, JSONB for raw payload flexibility, strong indexing, full-text search via pg_trgm. One DB for both raw and transformed data. |
| Auth (platform) | **Clerk** | Handles user login, org management, and JWT issuance. Language-agnostic — Go validates Clerk JWTs via JWKS endpoint. |
| Go HTTP framework | **Chi** | Lightweight, idiomatic, stdlib-compatible. Easy to add middleware and route groups as endpoints grow. |

### Not Decided Yet (TBD)

These decisions will be made when we begin low-level design. Do not implement against these until they are decided and documented here.

| Decision | Options Being Considered | Blocking On |
|----------|--------------------------|-------------|
| Job queue infrastructure | River (Postgres-native, no Redis), asynq (Redis-backed), in-process goroutine pool | LLD session |
| Go DB access layer | sqlc, pgx raw, GORM | LLD session |
| Hosting / compute | Railway, Fly.io, GCP Cloud Run, self-hosted VPS | Cost analysis |
| Provider auth model | API key (MVP), OAuth (V2) | LLD session |
| Search backend | Postgres full-text (MVP), Typesense/Elasticsearch (if needed) | Scale requirements |
| Frontend state / data fetching | TanStack Query, SWR, RSC-only | LLD session |

---

## 2. Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                            Clients                                    │
│                  Next.js Web App (frontend/)                         │
└────────────────────────────┬─────────────────────────────────────────┘
                             │ HTTPS / REST
┌────────────────────────────▼─────────────────────────────────────────┐
│                       Go API Server (backend/)                        │
│  • Auth middleware — verifies Clerk JWT on every request             │
│  • REST endpoints — activity feed, flag list, analytics              │
│  • Search service — translates query params → SQL                    │
│  • Webhook receiver — accepts real-time pushes from providers        │
│  • Polling scheduler — cron per active integration                   │
└──────────┬──────────────────────────────────────┬────────────────────┘
           │ Reads + Writes                        │ Spawns goroutines
┌──────────▼──────────────────┐       ┌────────────▼───────────────────┐
│       PostgreSQL             │       │   Async Worker (goroutines)    │
│  • raw_events               │◄──────│  • Normalize job               │
│  • flag_events              │ Write │  • Enrich job (rollback detect)│
│  • flags                    │       │  • Future: embedding job       │
│  • flag_state_snapshots     │       └────────────────────────────────┘
│  • organizations            │
│  • integrations             │
└──────────────────────────────┘
```

### Component Responsibilities

| Component | Owns | Does NOT own |
|-----------|------|--------------|
| `frontend/` | UI rendering, data display, user interaction | Business logic, direct DB access |
| `backend/` (API layer) | HTTP routing, auth, input validation, search translation | Transformation logic |
| `backend/` (worker layer) | Event normalization, enrichment, future embeddings | HTTP handling |
| `backend/` (extract layer) | Provider polling, webhook ingestion, raw event storage | Normalization |

The Go backend is a **single binary** that runs the API server, the polling scheduler, and the goroutine worker pool. They share the same process but have clear internal package boundaries.

---

## 3. ETL Pipeline — The Flow

This is the complete data flow. Every implementation decision must serve this flow.

```
Step 1: EXTRACT
  ├─ Polling (cron): Go scheduler triggers per integration → calls provider API
  └─ Webhook: Provider POSTs to our endpoint → received by Go HTTP handler

Step 2: RAW STORAGE
  └─ Raw payload stored as-is in raw_events table (JSONB)
     Deduplication: UNIQUE(integration_id, provider_event_id) — safe to receive twice

Step 3: ENQUEUE NORMALIZATION
  └─ After raw insert succeeds → dispatch normalize job (goroutine or queue — TBD)

Step 4: NORMALIZE (async worker)
  └─ Worker picks up job → calls provider-specific normalizer (pure function)
     → writes normalized flag_event row
     → upserts into flags registry
     → writes flag_state_snapshot if status changed

Step 5: ENRICH (async worker)
  └─ After normalize → dispatch enrich job
     → rollback detection (flag toggled back within short window)
     → update flags.last_event_at

Step 6: TRANSFORM — FUTURE
  └─ Call embedding model → store vector in flag_events for RAG

Step 7: SERVE
  └─ Client (UI) sends GET request to search/query endpoints

Step 8: SEARCH TRANSLATION
  └─ Go API parses + validates query params → builds parameterized SQL query

Step 9: DB QUERY
  └─ Go API executes query against PostgreSQL (always filtered by organizationId)

Step 10: RETURN DATA
  └─ Go API serializes result → returns JSON to UI

Step 11: RENDER
  └─ Next.js UI renders data
```

### Critical Invariants (never violate these)

- **Raw data is never discarded.** If normalization fails, the raw_event stays in the DB with a failed status. Re-process later.
- **Deduplication at the DB layer.** Webhook retries and polling overlaps are handled by the `UNIQUE` constraint on `raw_events`, not application logic.
- **Webhook handlers return immediately.** Never do synchronous processing inside a webhook handler. Store raw, dispatch job, return 200.
- **Every query filters by `organizationId` first.** No cross-tenant data leakage. Ever.

---

## 4. Provider Integration Design

All data sources — feature flag providers today, business metrics and code platforms tomorrow — must be treated as implementations of a **single interface**. The pipeline, queue, and storage layers must not contain any source-specific logic.

```
DataSource Interface (to be defined in LLD):
  fetchEvents(credentials, options) → []RawEvent
  normalizeEvent(raw) → NormalizedEvent | nil   ← pure function, no I/O
  parseWebhook(payload, headers, secret) → []RawEvent | nil
```

This interface must be expressive enough to cover fundamentally different source types: a feature flag audit log, a metrics alert stream, a GitHub webhook, a Snowflake query result. The normalized event shape is the design challenge — see Open Question Q3.

### Provider Availability

| Capability | LaunchDarkly | PostHog | Statsig |
|------------|-------------|---------|---------|
| Audit log API | Yes | Yes (activity_log) | Yes |
| Webhook push | Yes | No | No |
| Environment scoping | Yes (in spec field) | Yes (project-scoped) | Limited |
| Actor email | Yes (member.email) | Yes (user.email) | No (name only) |
| Automated change detection | Yes (token.serviceToken) | No | No |
| Action fidelity | High (titleVerb) | Medium (inferred from changes diff) | Low (inferred) |

### Normalization Notes (per provider)

**LaunchDarkly:**
- `titleVerb` is the most reliable action signal: `"turned on the flag"` → enabled, `"turned off the flag"` → disabled
- Environment parsed from `spec` field format: `proj/PROJECT:env/ENV:flag/KEY`
- Automated = `token.serviceToken === true`

**PostHog:**
- Action inferred from `detail.changes[]` — look for `field === "active"`, `new_value: true/false` → enabled/disabled
- No webhook — polling only

**Statsig:**
- Filter to `type === "GATE"` only (feature gates = feature flags)
- No reliable email — store display name only
- No webhook — polling only

---

## 5. Database Schema (High-Level)

Full DDL to be written during LLD. This section defines the tables and their purpose.

### Tables

**`organizations`** — One row per customer tenant. Primary isolation boundary.

**`integrations`** — One row per provider connection per org. Stores encrypted credentials, polling cursor, last sync time.

**`raw_events`** — Every event received from every provider, stored as JSONB. Never deleted. The source of truth.
- Key columns: `organization_id`, `integration_id`, `provider`, `provider_event_id` (for dedup), `raw_payload` (JSONB), `processing_status`

**`flags`** — Registry of every flag we've seen. Upserted from normalize jobs.
- Key columns: `organization_id`, `integration_id`, `flag_key`, `flag_name`, `first_seen_at`, `last_event_at`

**`flag_events`** — Normalized audit log. One row per meaningful event.
- Key columns: `organization_id`, `flag_id`, `raw_event_id`, `environment`, `action`, `actor_email`, `is_automated`, `change_details` (JSONB), `occurred_at`
- Actions: `created | updated | deleted | enabled | disabled | targeting_changed | archived | rollback`

**`flag_state_snapshots`** — Current and historical state of each flag per environment.
- Key columns: `flag_id`, `environment`, `status` (off/on/partial), `rollout_percentage`, `snapshotted_at`

### Indexing Strategy (to be finalized in LLD)
All common filter columns (`organization_id`, `occurred_at`, `environment`, `actor_email`, `action`, `flag_key`) must have indexes. Queries are written first; indexes follow the query plan.

---

## 6. API Design (High-Level)

Full endpoint spec to be written during LLD. This section defines the resource groups.

All endpoints require a valid Clerk JWT. All responses are `application/json`. All list responses are cursor-paginated (no offset pagination).

### Resource Groups

**`/integrations`** — CRUD for provider connections. Trigger manual sync.

**`/flags`** — List flags with filters (status, environment, stale, dead). Get flag detail. Get flag event history.

**`/events`** — Activity feed. Filterable by environment, time range, actor, action, provider, automated.

**`/analytics/summary`** — Aggregate counts: flags created/enabled/deleted/rolled back, stale count, dead count. Scoped by time period.

**`/analytics/trend`** — Time-series data for charts. Group by hour/day/week.

**`/analytics/velocity`** — Developer velocity: time from creation to prod launch, by flag and by actor.

**`/webhooks/:provider`** — Inbound webhook receiver. No auth header — verified by signature.

---

## 7. Product Requirements

### 7.1 MVP — "Lights On" (Ship First)

Goal: Make the data visible.

| Feature | Notes |
|---------|-------|
| Activity feed — who changed what, when | LD's `titleVerb` is cleanest; PostHog/Statsig need inference |
| Flag status breakdown — % off / on / partial | LD direct; PostHog/Statsig inferred from change diffs |
| Trend line graph — changes over time | Aggregate flag_events by day |
| Flag detail page — full history per flag | Filter flag_events by flag_id |
| Actor attribution — "last modified by" | LD: member.email; PostHog: user.email; Statsig: name only |
| Basic search by flag name + time range | Flag name fuzzy search + time window filter |
| % of flags idle / actively used / dead | Derived from last_event_at and current status |
| # of flags created / changed / launched / deleted | Count flag_events by action |

**"Launched"** = `action = 'enabled'` AND `environment = 'production'`
**"Dead"** = current status is `on`, last event was 60+ days ago, no changes since
**"Stale"** = last event was 30+ days ago, never reached `on`

### 7.2 P0 — Root Cause Analysis

Goal: When something breaks in production, these are the first things engineers reach for.

| Feature | Notes |
|---------|-------|
| "What changed in the last X hours?" scoped to prod | Filter flag_events by environment=production + time range |
| Automated vs. human change detection | `is_automated` field + filter |
| Rollback detection | Detect flag toggled back within short window — enrich job logic |
| Per-flag timeline view | Full event history ordered by occurred_at |
| Search by time range + actor + flag | Combine filters on flag_events |

### 7.3 P1 — Developer Velocity

Goal: Measure idea → production.

| Feature | Notes |
|---------|-------|
| Time from flag creation → turned on in prod | Compare created event vs first enabled event in prod |
| Stale flag list — created but never launched | Flags with no enabled event past 30 days |
| Dead flag list — launched 60+ days, never changed | Last event timestamp comparison |
| Flag cleanup suggestions | Combine stale + dead signals |
| Owner-level velocity | Group time-to-launch by actor_email |
| Launch rate — flags shipped per week | Count enabled events per time window |

### 7.4 P1 — Company Progress / Monthly Snapshot

Goal: "What happened this month?" for the whole company.

| Feature | Notes |
|---------|-------|
| Flags launched this month | Count enabled events in period |
| Flags cleaned up (deleted) | Count deleted events in period |
| Monthly summary card | X launched, Y cleaned up, Z created |
| Team/owner contribution breakdown | Group by actor_email |

---

## 8. Open Questions

These must be answered before or during the low-level design session. Do not make implementation decisions that depend on unresolved questions.

### Architecture

**Q1: Job queue infrastructure**
Options: River (Postgres-native, no Redis dependency), asynq (Redis-backed), in-process goroutine pool with a work channel.
- River is attractive because it eliminates Redis and enables transactional job enqueueing (enqueue in same DB transaction as raw_event insert).
- Trade-off: River adds complexity to Postgres; goroutine pool is simpler but loses durability across restarts.

**Q2: Database schema for raw data vs. transformed data**
- Same Postgres instance, separate tables? Separate schemas (`raw` schema vs `public`)? Separate databases?
- Recommendation leaning toward: same instance, same schema, clearly named tables (`raw_events` vs `flag_events`).

**Q3: Reusable provider interface**
- Go interface definition: what methods, what types? How do we handle provider-specific config (project keys, env keys) generically?
- How do we register new providers without touching pipeline code?

**Q4: How to design ETL components to scale for:**
1. Many different data sources (GitHub, Datadog, Snowflake in the future)
2. High request volume
3. Priority queuing for higher-tier customers

**Q5: Self-hosted enterprise path**
- Docker Compose for local/self-hosted: what services need to be swappable?
- Auth: Clerk is SaaS-only. Self-hosted needs Keycloak or raw OIDC. How do we abstract this?
- What changes in the architecture for a customer who wants everything on-prem?

### Auth

**Q6: Auth into the DataGill platform**
- Clerk is the current plan. Confirm this is final before building auth middleware.

**Q7: Provider credential model**
- MVP: customer generates read-only API key in their provider, pastes into DataGill. We encrypt at rest.
- V2: OAuth flow so customers don't handle keys manually.
- How do we store and rotate these credentials securely?

### Data

**Q8: Data retention policy**
- How long do we keep raw_events? flag_events?
- Is this per-plan (free = 90 days, pro = 1 year, enterprise = unlimited)?
- Archival strategy when data ages out?

**Q9: RAG / embeddings path**
- When we add OpenAI embeddings, what does the schema change look like?
- pgvector extension on flag_events? Separate embeddings table?
- This should be designed upfront so the schema doesn't need breaking changes later.

**Q10: Multi-environment handling**
- LaunchDarkly scopes environments clearly. PostHog and Statsig are less clear.
- How do we normalize "production" across providers where environment names differ?
- Do we let customers configure environment name mappings?

---

## 9. Roadmap Beyond MVP

These are planned future integrations and capabilities — they are on the roadmap, not out of scope. Do not build them yet, but **do not design the MVP in a way that makes them hard to add**.

### Future Data Sources (post-MVP)

| Source | Category | Example Questions Unlocked |
|--------|----------|---------------------------|
| Datadog | Business metrics | Did error rate spike when this flag was enabled? |
| Snowflake | Data warehouse | What was conversion rate during this flag's rollout? |
| GitHub | Code | Which PR introduced this flag? Time from commit → prod? |
| PagerDuty | Incidents | Was there an incident open when this flag was rolled back? |
| Jira / Linear | Project tracking | Which ticket drove this flag? Cycle time from ticket → launched? |

### Future Capabilities

- AI-generated summaries and natural language querying (RAG over normalized events)
- Email / Slack alerting (e.g. "a flag was rolled back in production")
- Public API for customers to query DataGill data programmatically
- Self-serve billing / plan management
- Cross-source correlation (flag change + metric anomaly on the same timeline)

---

## 10. Monorepo Structure (Tentative)

```
datagill-website/
├── frontend/          # Next.js app
│   └── ...
├── backend/           # Go — single binary: API + worker + scheduler
│   └── ...
├── CLAUDE.md          # This file
└── docker-compose.yml # Local development (TBD)
```

The Go backend is a **single binary**. The API server, the goroutine worker pool, and the polling scheduler all live in one process with clean internal package separation. They do not need to be separate services at this stage.

---

*Last updated: 2026-03-11*
*Next step: Low-level design session to resolve open questions in Section 8*
