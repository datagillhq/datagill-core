# Engineering Standards

Every agent and every team member must follow these standards without exception. They are not guidelines — they are requirements. When making any decision not covered here, consult the relevant section of *Designing Data-Intensive Applications* (DDIA) by Martin Kleppmann and record the decision in [architecture.md](./architecture.md) (or as an open question in [product.md](./product.md)).

## Go Server Development

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

## PostgreSQL

All data decisions are guided by *Designing Data-Intensive Applications* (DDIA). Chapter references are cited inline.

**Column strategy (DDIA Ch. 2 — Data Models)**

Use typed columns for anything you filter, sort, join, or index. Use JSONB for evolving, provider-specific, or flexible payload.

| Typed column | JSONB |
|-------------|-------|
| `org_id`, `integration_id`, `occurred_at` | `data` (normalized payload) |
| `state`, `event_type`, `source`, `key` | `config` (integration config) |
| `source_key`, `feature_flag_key` | Provider-specific sub-fields |

Rule: extend `data` JSONB first; promote a field to a typed column only when query performance requires it, and record the decision in [architecture.md](./architecture.md).

**Indexing (DDIA Ch. 3 — Storage and Retrieval)**
- B-tree (default): equality and range queries — `org_id`, `occurred_at`, `integration_id`
- GIN: JSONB containment queries (`data @>`) — add only when a slow query is observed and `EXPLAIN ANALYZE` confirms index benefit
- HNSW: vector ANN search — do not build during high-insert backfill (see the JSONB indexing notes in [architecture.md](./architecture.md#data-model))
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

## Monitoring

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

## Logging

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

## Clean Code

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
- Mark unresolved decisions with a reference: `// TODO(docs/product.md open questions): set rollback detection window`

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

## Git Workflow

**Never push directly to `main`.** Every change — including single-line fixes — must go through a branch and pull request. There are no exceptions.

- Branch naming: `<initials>/<short-description>` (e.g. `oz/fix-dockerfile-build`)
- Every PR must pass tests and lint before merging
- A passing CI check is not a substitute for a PR — the PR is where review, context, and history live

**Why:** A single-line change can cause an incident. Direct pushes to `main` bypass review, skip the audit trail, and cannot be reverted cleanly if the branch was never created.

## DDIA Principles

*Designing Data-Intensive Applications* (Kleppmann) is the primary architectural reference for all storage, consistency, and data flow decisions. When a design decision is not covered by this document, consult the relevant DDIA chapter and add the decision to [architecture.md](./architecture.md).

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

*Last updated: 2026-11-13*
*Status: v1.2*
