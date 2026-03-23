# Implementation Plan

This document is the execution guide for building DataGill. Architecture and product decisions
live in `AGENTS.md` — read that first. This document tells you *how* to build it, *in what order*,
and *who can work on what simultaneously*.

---

## Finalized Tool Decisions

These are additions to the stack decided in `AGENTS.md`:

| Concern | Choice | Rationale |
|---------|--------|-----------|
| Structured logging | **zerolog** | Zero-allocation, JSON output, context propagation via `zerolog.Ctx(ctx)` |
| Log aggregation | **Grafana Loki + Promtail** | Docker-native log collection; same Grafana instance as Prometheus — one UI for metrics + logs |
| Linter | **golangci-lint** | Standard Go; single config covers errcheck, staticcheck, govet, unused, exhaustive |
| Integration test DB | **testcontainers-go** | Spins up real Postgres in tests — no mocks, no persistent state between runs |
| Temporal workflow tests | **`go.temporal.io/sdk/testsuite`** | Official Temporal test framework; fast, no real server needed for unit tests |

---

## Cloud Strategy (Cost-Conscious)

We will eventually move to cloud, but budget is limited. Every infrastructure decision must have a local dev form (Docker Compose) and a cloud form (cheapest viable option). The two forms must be identical in behavior — only the host/provider changes.

**Guiding principle:** self-host what is cheap to run, use managed services only where operational cost (time) exceeds the dollar cost. Never pay for a managed service that adds < 1 hour/month of operational burden to self-host.

### Postgres — one RDS instance, three databases

The two Docker Compose Postgres containers (`temporal-postgres`, `app-postgres`) map to a **single RDS instance** in cloud. Temporal requires separate databases (not schemas — its tooling owns them by name), so one instance holds three databases:

```
RDS instance  (one bill)
├── temporal              ← Temporal workflow state
├── temporal_visibility   ← Temporal UI / search
└── datagill              ← All application tables
```

- Start with `db.t4g.micro` (~$12/mo) — upgrade to `db.t4g.small` (~$25/mo) when needed
- No Multi-AZ at MVP — add when the business requires the uptime guarantee
- No RDS Proxy at MVP — add when connection pool pressure appears
- Docker Compose keeps two separate containers locally (simpler dev setup) — cloud uses one instance with two connection string env vars pointing to the same host, different database names

### Temporal — self-hosted, not Temporal Cloud

Temporal Cloud costs ~$25+/mo minimum. Self-hosting Temporal on the same compute as the worker is free. At our workflow volume (one ETL run per integration every 5–60 min), self-hosted Temporal on a single small instance is more than sufficient.

Do not switch to Temporal Cloud unless operational burden of self-hosting becomes significant (outages, upgrade pain, on-call load).

### Monitoring and logging — Grafana Cloud free tier

In cloud, do **not** self-host Prometheus, Grafana, and Loki on a separate instance. Use **Grafana Cloud free tier** instead:

| What | Free tier limit | Sufficient for MVP? |
|------|----------------|---------------------|
| Prometheus metrics | 10,000 series | Yes — we have ~50–100 series |
| Loki logs | 50 GB/month | Yes |
| Grafana dashboards | Unlimited | Yes |

Services push metrics to Grafana Cloud (remote_write) instead of a local Prometheus. Logs ship directly to Loki via the Grafana Cloud endpoint instead of a self-hosted Loki. No separate monitoring instance to pay for.

Local dev keeps the self-hosted stack (Prometheus + Grafana + Loki via Docker Compose) — identical dashboards, different data destination.

### Secrets — SSM Parameter Store, not Secrets Manager

AWS Secrets Manager costs $0.40/secret/month. SSM Parameter Store **Standard** tier is free for up to 10,000 parameters. Use SSM for all secrets at MVP. Secrets Manager is only worth it for automatic rotation — a feature we don't need until we have customers with compliance requirements.

### Compute — start with one host

At MVP, all application services (Worker + Temporal server + Search Service) run on a single EC2 instance or ECS Fargate task. Split onto separate hosts only when resource contention is observed or independent scaling is needed.

Recommended starting point: `t4g.small` (2 vCPU, 2 GB RAM) — ~$12/mo on-demand, ~$7/mo with a 1-year reserved instance. Upgrade path: `t4g.medium` → separate hosts.

### Estimated MVP cloud cost

| Service | Option | Monthly cost |
|---------|--------|-------------|
| Compute (worker + temporal + search) | 1× `t4g.small` EC2 | ~$12 |
| Database | 1× `db.t4g.micro` RDS (3 databases) | ~$12 |
| Monitoring + logging | Grafana Cloud free tier | $0 |
| Secrets | SSM Parameter Store standard | $0 |
| Networking (data transfer, etc.) | Estimate | ~$5 |
| **Total** | | **~$29/mo** |

### Local dev → cloud mapping

| Docker Compose service | Cloud equivalent |
|-----------------------|-----------------|
| `temporal-postgres` | RDS instance (database: `temporal`) |
| `app-postgres` | RDS instance (database: `datagill`) |
| `temporal` | Self-hosted on compute instance |
| `temporal-ui` | Self-hosted on compute instance |
| `prometheus` | Grafana Cloud (remote_write endpoint) |
| `grafana` | Grafana Cloud |
| `loki` + `promtail` | Grafana Cloud Loki endpoint |
| `worker` | ECS Fargate task / EC2 |
| `search` | ECS Fargate task / EC2 |

The Docker Compose file is the source of truth for service configuration. Moving to cloud is a matter of swapping env vars and connection strings — no code changes.

---

## Current Repo State (as of plan creation)

The repo was scaffolded for an older 3-service architecture (ingestion, processing, search + BullMQ).
The new architecture is a Temporal Worker + Search Service. Work in Phase 0 reconciles this.

| Item | Status | Action |
|------|--------|--------|
| `services/ingestion/` | Obsolete | Delete |
| `services/processing/` | Obsolete | Delete |
| `services/search/` | Partial — keep Chi scaffolding, replace internals | Refactor in Phase 2 |
| `ingestion/`, `processing/` (root-level) | Obsolete | Delete |
| `go.work` | References old services | Update |
| `docker-compose.yml` | Missing all infra services | Replace |
| `Makefile` | Incomplete | Extend |
| `.github/` | Missing | Add in Phase 0 |
| `cmd/worker/` | Missing | Add in Phase 1 |
| `models/`, `repository/`, `activities/`, `workflows/` | Missing | Add in Phase 1 |

---

## Phase Overview

```
Phase 0: Repo Reset & Tooling         ← 1 person, unblocks everything, ~0.5 day
    │
    ├── Phase 1A: Infrastructure       ← Stream A, parallel
    ├── Phase 1B: DB & Shared Layer    ← Stream B, parallel
    └── Phase 1C: Search Scaffold      ← Stream C, parallel
            │           │
            │       Phase 2A: ETL Worker       ← Stream D, after 1B
            └───── Phase 2B: Search Impl       ← Stream E, after 1B + 1C
                        │           │
                        └─── Phase 3: Integration & Hardening  ← whole team
                                    │
                              Phase 4: Frontend  ← can start after Phase 1C
```

---

## Phase 0: Repo Reset & Tooling

**Owner:** 1 person. Must complete before any other phase starts — structural changes cause merge conflicts.

### Tasks

**0.1 — Remove obsolete services and proto scaffolding**
- Delete `services/ingestion/`, `services/processing/`
- Delete root-level `ingestion/`, `processing/` directories
- Delete `buf.yaml`, `buf.gen.yaml`, `proto/`, `shared/gen/`
- Remove `google.golang.org/protobuf` dependency from `services/search/go.mod` and `shared/go.mod`
- Remove proto-generated imports from `services/search/handlers.go` (ping handler uses proto types)
- Update `go.work` to reference only `./services/search` and `./shared`
- Verify `go build ./...` passes

**0.2 — Add `cmd/worker/` module**
- Create `cmd/worker/go.mod` (`module github.com/datagill/datagill-core/worker`)
- Add to `go.work`
- Skeleton `cmd/worker/main.go` that compiles (can just print a TODO)

**0.3 — Add `models/` and `repository/` packages**
- Create `models/go.mod` (`module github.com/datagill/datagill-core/models`)
- Create `repository/go.mod` (`module github.com/datagill/datagill-core/repository`)
- Both skeleton packages, just enough to compile
- Add both to `go.work`

**0.4 — golangci-lint**
- Add `.golangci.yml` at repo root (see config spec below)
- Update `Makefile` target `lint` to call `golangci-lint run ./...`
- Verify lint passes on current code before committing

**0.5 — GitHub Actions CI**
- `.github/workflows/ci.yml`: triggers on PR + push to main
- Jobs: `lint` (golangci-lint), `test` (go test ./...), `build` (go build ./...)
- Jobs run in parallel — none depend on each other
- Use `actions/setup-go` with Go version matching `go.work`
- Cache Go module downloads

**0.6 — Extend Makefile**
```
lint          golangci-lint run ./...
test          go test ./...
test-unit     go test -short ./...
test-int      go test -run Integration ./...
build         go build all services + worker
docker-up     docker compose up -d
docker-down   docker compose down
migrate       run golang-migrate up against app-postgres
```

### golangci-lint Config Spec

```yaml
# .golangci.yml
linters:
  enable:
    - errcheck       # never silently ignore errors
    - govet          # correctness
    - staticcheck    # advanced static analysis
    - unused         # dead code
    - gofmt          # formatting
    - goimports      # import ordering
    - exhaustive     # switch on enums must cover all cases
    - noctx          # HTTP requests must use context
    - godot          # exported comments end with period
    - wrapcheck      # errors crossing package boundaries must be wrapped

linters-settings:
  exhaustive:
    default-signifies-exhaustive: false
```

**0.7 — Claude Code agent skills**
- Create `.claude/commands/` directory at repo root
- Create the three slash commands below — these are available to every team member and every agent working in this repo
- Add a note in the root `README.md` listing the available commands

| Command | File | Purpose |
|---------|------|---------|
| `/add-provider` | `.claude/commands/add-provider.md` | Step-by-step guide for implementing a new feature flag provider |
| `/pr-checklist` | `.claude/commands/pr-checklist.md` | Pre-PR verification checklist against all engineering standards |
| `/new-migration` | `.claude/commands/new-migration.md` | Guide for creating a new DB migration following repo conventions |

---

## Phase 1A: Infrastructure

**Owner:** 1 person (DevOps/infra focus). Fully parallel to 1B and 1C.

**Goal:** Full `docker compose up` brings up every infra dependency; Grafana shows metrics and logs.

> **Cloud note:** Docker Compose is local dev only. See the Cloud Strategy section above for what each service maps to in cloud. Keep env var names consistent between Docker Compose and cloud — the only thing that changes is the values.

### Tasks

**1A.1 — Replace docker-compose.yml**

Services to include:

| Service | Image | Port | Notes |
|---------|-------|------|-------|
| `temporal-postgres` | `postgres:16` | (internal) | Temporal's own DB. Env: `POSTGRES_DB=temporal` |
| `app-postgres` | `postgres:16-alpine` with pgvector | 5432 | App data. Use `pgvector/pgvector:pg16` image |
| `temporal` | `temporalio/auto-setup:latest` | 7233 | Depends on `temporal-postgres` healthy |
| `temporal-ui` | `temporalio/ui:latest` | 8080 | Points to `temporal:7233` |
| `prometheus` | `prom/prometheus:latest` | 9090 | Mounts `./infra/prometheus/prometheus.yml` |
| `grafana` | `grafana/grafana:latest` | 3000 | Mounts provisioning configs |
| `loki` | `grafana/loki:latest` | 3100 | Mounts `./infra/loki/loki.yml` |
| `promtail` | `grafana/promtail:latest` | (internal) | Reads Docker socket, ships logs to Loki |
| `search` | `./services/search/Dockerfile` | 8082 | Depends on `app-postgres` healthy |
| `worker` | `./cmd/worker/Dockerfile` | (none) | Depends on `app-postgres` + `temporal` healthy |

Rules:
- All services use `restart: unless-stopped`
- `app-postgres` and `temporal-postgres` use named volumes for data persistence
- Services that depend on Postgres use `healthcheck` + `depends_on: condition: service_healthy`
- No hardcoded passwords — use `.env` file (add `.env` to `.gitignore`, provide `.env.example`)
- Network: all services on a single `datagill` bridge network

**1A.2 — Prometheus config**

File: `infra/prometheus/prometheus.yml`
- Scrape interval: 15s
- Scrape targets: `search:8082/metrics`, `worker:9090/metrics` (worker exposes its own /metrics HTTP server)
- Add job labels: `job: search`, `job: worker`

**1A.3 — Grafana provisioning**

Directory: `infra/grafana/`
- `provisioning/datasources/datasources.yml` — Prometheus + Loki datasources, preconfigured, no manual setup
- `provisioning/dashboards/dashboards.yml` — dashboard provider pointing to `./dashboards/`
- `dashboards/search-service.json` — starter dashboard: request rate, p99 latency, error rate, in-flight
- `dashboards/worker.json` — starter dashboard: workflow start/complete/fail, activity duration

**1A.4 — Loki + Promtail config**

File: `infra/loki/loki.yml` — local filesystem storage, no auth, single-binary mode.
File: `infra/promtail/promtail.yml` — scrape Docker containers via `/var/run/docker.sock`,
add labels `service`, `container_name`. Parse JSON log lines (zerolog outputs JSON).

**1A.5 — Verify end-to-end**
- `docker compose up -d` starts cleanly
- Grafana at `:3000` shows Prometheus and Loki as green datasources
- Temporal UI at `:8080` shows healthy cluster
- `psql` connects to `app-postgres` successfully

---

## Phase 1B: DB & Shared Layer

**Owner:** 1 person (Go/data focus). Fully parallel to 1A and 1C.

**Goal:** Migration, canonical models, and repository layer are done and tested. Other phases import these.

### Tasks

**1B.1 — DB Migration**

File: `db/migrations/001_initialize.sql`
- Enable `uuid-ossp` extension
- Create all 5 tables in dependency order: `organizations`, `integrations`, `source_records`, `feature_flags`, `feature_flag_changelog`
- All columns, constraints, and indexes exactly as specified in `AGENTS.md` Section 6
- No FK constraints (per architecture decision)
- `pgvector` extension deferred — do not include `vector` columns yet

File: `db/migrations/002_indexes.sql`
- All secondary indexes from `AGENTS.md` (org_id, integration_id, occurred_at, etc.)
- Kept separate from table creation for clarity

Makefile target `migrate` runs: `migrate -path db/migrations -database $APP_DATABASE_URL up`

**1B.2 — `models/` package**

One file per concern:

`models/canonical.go`
- `FeatureFlag` struct (maps to `feature_flags` table)
- `FeatureFlagData` struct (maps to `feature_flags.data` JSONB) — all fields from normalized data model in `AGENTS.md`
- `FeatureFlagChangelog` struct (maps to `feature_flag_changelog` table)
- `ChangelogData` struct (maps to `feature_flag_changelog.data` JSONB)
- State enums: `FlagState`, `LifecyclePurpose`, `LifecycleState`, `EventType`

`models/integration.go`
- `Integration` struct
- `Source` enum: `SourceLaunchDarkly`, `SourceUnleash`, `SourceFlagsmith`
- `IntegrationConfig` struct (JSONB fields: project_key, environment_key, poll_interval_minutes, backfill_days)

`models/source_record.go`
- `SourceRecord` struct
- `SourceType` enum: `SourceTypeAuditLog`, `SourceTypeFlagDetail`

`models/launchdarkly.go`
- `LDAuditEntry` struct — mirrors LD audit log API response
- `LDFlagDetail` struct — mirrors LD GET flag detail API response
- These are raw types; no business logic here

`models/keys.go`
- `DataGillNamespace` UUID constant
- `ComputeFlagKey(source, envKey, projectKey, sourceFlagKey string) uuid.UUID`
- `ComputeSourceRecordKey(source, sourceType, sourceKey string) uuid.UUID`
- `ComputeChangelogKey(source, envKey, projectKey, sourceKey string) uuid.UUID`

Unit tests (`models/keys_test.go`):
- Table-driven tests for deterministic key computation
- Verify same inputs always produce same UUID
- Verify different inputs produce different UUIDs

**1B.3 — `repository/` package**

`repository/repository.go`
- `Repository` interface:
  ```go
  type Repository interface {
      SaveSourceRecord(ctx context.Context, r models.SourceRecord) error
      SaveFeatureFlag(ctx context.Context, f models.FeatureFlag) error
      SaveChangelog(ctx context.Context, c models.FeatureFlagChangelog) error
      GetIntegration(ctx context.Context, integrationID uuid.UUID) (models.Integration, error)
      ListIntegrations(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error)
  }
  ```
- `PostgresRepository` struct implementing `Repository` via `pgx/v5`
- All upserts use `ON CONFLICT (key) DO UPDATE SET ...`
- All queries filter by `org_id` first (enforced by query structure, not just convention)
- Constructor: `NewPostgresRepository(ctx, connString) (*PostgresRepository, error)`

> **TBD dependency:** Go DB access layer (sqlc vs pgx raw vs GORM) is an open question in `AGENTS.md`.
> Use **pgx/v5 raw** for now — it's the lowest-level correct choice. Switch is isolated to this package.

Integration tests (`repository/repository_test.go`):
- Use `testcontainers-go` to spin up `pgvector/pgvector:pg16`
- Run migrations against it before test suite (`TestMain`)
- Test each repository method: insert → verify, upsert duplicate → verify idempotent
- Tag with `//go:build integration` so `go test -short` skips them

---

## Phase 1C: Search Service Scaffold

**Owner:** 1 person. Fully parallel to 1A and 1B.

**Goal:** Search service compiles, runs, passes lint, has correct route structure and middleware.
Uses an in-memory repository so frontend work can proceed before Postgres is wired up.

### Tasks

**1C.1 — Switch logger from `slog` to `zerolog`**
- Replace `log/slog` with `github.com/rs/zerolog` throughout `services/search/`
- Root logger created in `main.go` with `service: search`, timestamp format Unix
- Logger stored on `app` struct; passed to handlers via context using `zerolog.Ctx(ctx)`

**1C.2 — Middleware stack**

In `router.go`, build the middleware chain in this order:
1. `RequestID` — generate UUID v4 request ID, set `X-Request-Id` response header
2. zerolog request logger — log `method`, `path`, `request_id`, `status`, `duration_ms` at request end
3. `RealIP` — trust `X-Forwarded-For` from trusted proxies
4. `Recoverer` — catch panics, log with stack trace, return 500
5. `Timeout(30s)` — cancel context after 30s
6. `OrgID` — extract `X-Org-Id` header, add to context and zerolog context logger
7. Prometheus middleware — record `http_requests_total` (by handler+method+status) and `http_request_duration_seconds` histogram

**1C.3 — Route structure**

Match `AGENTS.md` Section 5 exactly:
```
GET /healthz
GET /metrics                         (Prometheus)
GET /search                          (structured or semantic)
GET /flags                           (list, filter by source/environment)
GET /flags/{flag_key}                (flag detail + history summary)
GET /flags/{flag_key}/history        (paginated history, date range)
GET /timeline                        (unified event timeline for org)
```
Remove `/ping` and protobuf dependency (proto/ping was scaffolding, no longer needed).

**1C.4 — Repository interface**

Define `SearchRepository` interface in `services/search/repository.go`:
```go
type SearchRepository interface {
    Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
    ListFlags(ctx context.Context, orgID string, filters FlagFilters) ([]FlagSummary, error)
    GetFlag(ctx context.Context, orgID, flagKey string) (FlagDetail, error)
    GetFlagHistory(ctx context.Context, orgID, flagKey string, p Pagination) ([]ChangelogEntry, error)
    GetTimeline(ctx context.Context, orgID string, p Pagination) ([]ChangelogEntry, error)
}
```
`InMemorySearchRepository` implementation with realistic demo data (3–5 flags, 10–20 changelog entries).
Demo data should exercise the full response shape so frontend can build against it.

**1C.5 — Handler stubs**

Each route has a handler that calls the repository and returns JSON. Handlers log the org_id and request_id from context. No business logic yet — just wire request → repository → response.

**1C.6 — Unit tests**

Table-driven tests for each handler using `net/http/httptest` and the in-memory repository.
Test: happy path, missing org_id, invalid params, repository error.

---

## Phase 2A: ETL Worker

**Owner:** 1–2 people. Starts after Phase 1B completes.

**Goal:** Full LaunchDarkly ETL pipeline runs end-to-end via Temporal.

### Tasks

**2A.1 — LaunchDarkly HTTP client**

File: `activities/launchdarkly_client.go`
- Thin wrapper around `net/http` — no SDK dependency
- Methods: `ListAuditLog(ctx, before, limit)`, `GetFlagDetail(ctx, projectKey, flagKey)`
- Exponential backoff on 429/5xx with `Retry-After` header support
- Timeout: 10s per request
- Client constructed with API key + base URL (injectable for tests)
- Unit tests: table-driven with `httptest.NewServer` mocking LD responses

**2A.2 — Extract activities**

File: `activities/launchdarkly.go`

`ListAuditLogs(ctx, IntegrationID, before time.Time) ([]LDAuditEntry, error)`
- Calls LD client paginating until empty page
- Each page: saves raw response to `source_records` via `SaveSourceRecord`
- Returns all entries concatenated
- Calls `Continue-As-New` signal is handled by the workflow, not this activity

`GetFlagDetail(ctx, projectKey, flagKey string) (LDFlagDetail, error)`
- Single LD API call
- Saves raw response to `source_records`
- Returns deserialized detail

**2A.3 — Shared save activities**

File: `activities/shared.go`

`SaveSourceRecord(ctx, SourceRecord) error` — upserts via repository
`SaveChangelog(ctx, FeatureFlagChangelog) error` — upserts via repository
`SaveFeatureFlag(ctx, FeatureFlag) error` — upserts via repository

All three are idempotent (upsert on deterministic key).

**2A.4 — Normalize activities**

File: `activities/launchdarkly.go` (continued)

`NormalizeChangelog(ctx, sourceRecordKey, raw LDAuditEntry) (FeatureFlagChangelog, error)`
- Pure transformation: `LDAuditEntry` → `FeatureFlagChangelog` + `ChangelogData`
- `titleVerb` → `EventType` mapping (document the full mapping table in code comments)
- Actor extraction → `actor_email`
- Rollback detection: flag toggled back to previous state within 30-min window — query last changelog entry for this flag+env
- Automated detection: `token.serviceToken != nil` → `is_automated = true`

`NormalizeFlagDetail(ctx, sourceRecordKey, raw LDFlagDetail) (FeatureFlag, error)`
- Pure transformation: `LDFlagDetail` → `FeatureFlag` + `FeatureFlagData`
- Populate all `environments` entries
- `lifecycle.purpose` defaults to `temporary`; `lifecycle.state` defaults to `active`

Unit tests for both normalize functions: table-driven, no external deps. Cover edge cases
(missing actor, missing environment, automated token, rollback scenario).

**2A.5 — Workflows**

File: `workflows/etl.go`
- `FeatureFlagETLWorkflow(ctx, IntegrationID)` — queries DB for integration, routes to provider-specific extract workflow by `integration.source`

File: `workflows/launchdarkly_extract.go`
- `LaunchDarklyExtractWorkflow(ctx, Integration)`:
  1. `ListAuditLogs` activity (sequential pagination, cursor from workflow state)
  2. Deduplicate flag keys from results
  3. `GetFlagDetail` for each unique flag key (parallel via `workflow.Go`)
  4. Fan-out: for each audit entry → spawn `LaunchDarklyChangelogTransformWorkflow` (fire-and-forget + ack)
  5. Fan-out: for each unique flag → spawn `LaunchDarklyFeatureFlagTransformWorkflow` (fire-and-forget + ack)
  6. `Continue-As-New` after 100 pages

File: `workflows/launchdarkly_transform.go`
- `LaunchDarklyChangelogTransformWorkflow(ctx, sourceRecordKey, raw LDAuditEntry)`: calls `NormalizeChangelog` → `SaveChangelog`
- `LaunchDarklyFeatureFlagTransformWorkflow(ctx, sourceRecordKey, raw LDFlagDetail)`: calls `NormalizeFlagDetail` → `SaveFeatureFlag`

Workflow unit tests using `testsuite.WorkflowTestSuite`:
- Mock all activities
- Test routing logic in `FeatureFlagETLWorkflow`
- Test pagination + Continue-As-New trigger at 100 pages
- Test fire-and-forget fan-out (verify child workflows started)

**2A.6 — Worker registration**

File: `cmd/worker/main.go`
- Read config from env: `TEMPORAL_HOST`, `APP_DATABASE_URL`, `LD_API_KEY`, `LOG_LEVEL`
- Initialize zerolog logger
- Connect to Temporal
- Connect to Postgres (via repository)
- Register all workflows and activities
- Start worker on task queue `datagill-etl`
- Expose `GET /metrics` on `:9090` for Prometheus scrape (separate HTTP server)
- Graceful shutdown on SIGINT/SIGTERM

**2A.7 — Integration test**

File: `cmd/worker/integration_test.go`
- `testcontainers-go` for Postgres (run migrations)
- Real Temporal dev server (`go.temporal.io/server/temporaltest`)
- Seed one integration row
- Execute `FeatureFlagETLWorkflow` with a mocked LD HTTP server
- Assert rows written to `source_records`, `feature_flags`, `feature_flag_changelog`
- Assert idempotency: run same workflow twice, row counts unchanged

---

## Phase 2B: Search Service Implementation

**Owner:** 1 person. Starts after Phase 1B and 1C complete.

**Goal:** Search service queries real Postgres; all endpoints return correct data.

### Tasks

**2B.1 — PostgresSearchRepository**

File: `services/search/repository_postgres.go`
- Implement `SearchRepository` against real Postgres using `pgx/v5`
- `Search`: structured search via `feature_flag_changelog.data @> '{"environment":"prod"}'` + text matching on `feature_flags.data->>'name'`; filter by date range
- `ListFlags`: query `feature_flags` with optional `source` and `environment` filters
- `GetFlag`: join `feature_flags` + latest `feature_flag_changelog` entry
- `GetFlagHistory`: query `feature_flag_changelog` for flag key, ordered by `occurred_at DESC`, paginated
- `GetTimeline`: query `feature_flag_changelog` for org, ordered by `occurred_at DESC`, paginated
- All queries have `org_id = $1` as the first predicate — never omit this

**2B.2 — Wire Postgres into app**

Update `config.go` to read `APP_DATABASE_URL` from env.
Update `main.go`: if `APP_DATABASE_URL` set → use `PostgresSearchRepository`; else → use `InMemorySearchRepository` (keeps local dev without DB working).

**2B.3 — Prometheus metrics**

Metrics to expose (all labelled by `handler`):
- `datagill_http_requests_total` counter (method, handler, status_code)
- `datagill_http_request_duration_seconds` histogram (method, handler)
- `datagill_db_query_duration_seconds` histogram (query_name)
- `datagill_search_results_returned` histogram (handler)

Register all metrics in `metrics.go`; inject `MetricsRecorder` interface into repository and handlers.

**2B.4 — Zerolog context propagation**

Every log line inside a handler must carry `request_id` and `org_id`.
Pattern:
```go
log := zerolog.Ctx(ctx).With().Str("handler", "search").Logger()
log.Info().Str("query", q).Msg("search request")
```
The middleware (from Phase 1C) already sets `request_id` and `org_id` on the context logger.
Handlers must use `zerolog.Ctx(ctx)` — never create a new logger in a handler.

**2B.5 — Integration tests**

File: `services/search/integration_test.go`
- `testcontainers-go` for Postgres (run migrations, seed test data)
- Start real HTTP server on random port
- Test each endpoint: correct response shape, org_id scoping (org A cannot see org B's data), pagination, date range filters
- Test 404 for unknown flag key

---

## Phase 3: Integration & Hardening

**Owner:** Whole team. Sequential — do not start until Phases 2A and 2B are complete.

### Tasks

**3.1 — Multi-stage Dockerfiles**

`services/search/Dockerfile`:
```
Stage 1 (builder): golang:1.24-alpine — go build
Stage 2 (final): gcr.io/distroless/static-debian12 — copy binary only
```
`cmd/worker/Dockerfile`: same pattern.

Rules for both:
- No secrets in image layers
- `HEALTHCHECK CMD ["/bin/wget", "-qO-", "http://localhost:PORT/healthz"]` (search only; worker exposes /metrics not /healthz)
- Run as non-root user

**3.2 — Add worker + search to Docker Compose**

Both services built from Dockerfiles. Environment variables from `.env` file.
`depends_on` with `condition: service_healthy` for Postgres and Temporal.

**3.3 — End-to-end smoke test**

Manual runbook (document in `docs/e2e-test.md`):
1. `docker compose up -d`
2. Insert one integration row into `app-postgres` with a real LD API key
3. Trigger `FeatureFlagETLWorkflow` via Temporal UI or CLI
4. Verify rows appear in `feature_flags` and `feature_flag_changelog`
5. `curl localhost:8082/flags` → verify flags returned
6. Open Grafana `:3000` → verify metrics scraped, logs in Loki

**3.4 — Load test**

Tool: `hey` or `k6` (agent's choice).
Target: `GET /flags` and `GET /search?q=checkout` at 50 req/s.
Pass criteria: p99 < 500ms at 50 req/s with 1000 flags in DB.
Document results in `docs/load-test-results.md`.

**3.5 — Adding a new provider (runbook)**

Document in `docs/add-provider.md`:
1. Add `Source` enum value in `models/integration.go`
2. Add raw types in `models/{provider}.go`
3. Add HTTP client + activities in `activities/{provider}.go`
4. Add extract + transform workflows in `workflows/{provider}_extract.go`, `workflows/{provider}_transform.go`
5. Add routing case in `workflows/etl.go`
6. Register activities + workflows in `cmd/worker/main.go`
7. Zero changes to `services/search/` or existing provider code

---

## Phase 4: Frontend

**Owner:** 1 person. Can start after Phase 1C (in-memory Search Service is enough to build against).

### Tasks

**4.1 — Next.js setup**
- `npx create-next-app@latest frontend --typescript --tailwind --app` inside `frontend/`
- Install shadcn/ui: `npx shadcn@latest init`
- Install Clerk: `@clerk/nextjs`
- Add `frontend/` to repo root, add to `.gitignore` entries for `node_modules`

**4.2 — Auth**
- `middleware.ts` with Clerk `clerkMiddleware()` — protect all routes under `/dashboard`
- `ClerkProvider` in root layout
- Sign-in page at `/sign-in`

**4.3 — Pages (in priority order)**
1. `/dashboard/flags` — flag list, filterable by source/environment
2. `/dashboard/flags/[flag_key]` — flag detail + change history
3. `/dashboard/timeline` — unified event timeline
4. `/dashboard/search` — search bar, results list

**4.4 — API integration**
- All fetch calls go through a thin `lib/api.ts` client that:
  - Reads `NEXT_PUBLIC_SEARCH_SERVICE_URL` from env
  - Attaches Clerk JWT as `Authorization: Bearer <token>`
  - Attaches org ID as `X-Org-Id` header
- No direct DB access from frontend — all reads go through Search Service

**4.5 — Dockerize**
- `frontend/Dockerfile`: multi-stage, Node builder → `node:alpine` final
- Add `frontend` service to Docker Compose (port 3000, depends on `search`)

---

## Standards Every Agent Must Follow

### Error Handling
- Wrap errors with context: `fmt.Errorf("ListAuditLogs: %w", err)`
- Never use `_` to discard errors
- Temporal activities: return the error — Temporal handles retry. Never `panic`.
- HTTP handlers: log the error (with context), return a generic message to the client

### Context Propagation
- `ctx context.Context` is always the first argument to every function that does I/O
- Pass context to all DB queries, HTTP calls, and Temporal activities
- Zerolog: always use `zerolog.Ctx(ctx)` in handlers/activities — never create a new logger

### Testing
- Unit tests: no external deps, table-driven, fast. Run with `go test -short ./...`
- Integration tests: build tag `//go:build integration`. Run with `go test -run Integration ./...`
- Temporal workflow unit tests: use `testsuite.WorkflowTestSuite` (no server needed)
- Never mock the DB in integration tests — use testcontainers-go

### Idempotency
- Every write must be safe to run twice. Use `ON CONFLICT (key) DO UPDATE SET ...`
- Verify in integration tests: run same operation twice, assert identical final state

### Configuration
- All config via environment variables — no hardcoded values
- Provide `.env.example` with every env var documented
- Fail fast on startup if required env vars are missing

### Commit hygiene
- One logical change per PR
- PR description must reference the phase + task (e.g. "Phase 2A.4 — normalize activities")
- All PRs must pass CI (lint + unit tests) before merge
- Integration tests run on merge to main (not on every PR — they're slow)

---

## Dependency Graph (summary)

```
Phase 0  ──────────────────────────────────────────────────────► unblocks all
              │                    │                    │
          Phase 1A             Phase 1B             Phase 1C
         (Infra/Docker)     (DB + models +        (Search scaffold +
                             repository)           in-memory repo)
                                  │                    │
                        ┌─────────┘         ┌──────────┘
                        │                   │
                    Phase 2A            Phase 2B
                   (ETL Worker)      (Search Postgres impl)
                        │                   │
                        └────────┬──────────┘
                                 │
                            Phase 3
                        (Integration + hardening)
                                 │
                            Phase 4
                           (Frontend)
                     (can overlap with 2A/2B
                      using Phase 1C stub)
```

---

*Created: 2026-03-23*
*Architecture reference: AGENTS.md v1.0*
