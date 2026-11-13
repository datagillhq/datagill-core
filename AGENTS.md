# AGENTS.md

Instructions for coding agents and contributors working in `datagill-core`. Read this before
changing anything. Longer background lives in `docs/`:

| Doc | Read it when |
|-----|--------------|
| [`docs/architecture.md`](docs/architecture.md) | Changing workflows, the data model, or anything with a trade-off. Explains how the system works and why. |
| [`docs/engineering-standards.md`](docs/engineering-standards.md) | Writing Go, SQL, logs, or metrics. The full standards; the rules below are the summary. |
| [`docs/product.md`](docs/product.md) | Building a feature. Requirements, roadmap, and **open questions**. |
| [`README.md`](README.md) | You need the current feature list or the quick start. |

## What this repo is

The backend for DataGill. It ingests engineering events (LaunchDarkly flag changes, GitHub
merged PRs), normalizes them into Postgres, enriches them (flag keys found in PR diffs), and
serves analytics. The web UI and auth server are a separate repo, `datagill-ui`.

Two binaries, sharing only Postgres and Temporal:

- **Worker** (`cmd/worker`): runs every Temporal workflow and activity.
- **Search service** (`services/search`): HTTP API on `:8082`. Auth, integrations, schedules,
  analytics. No sync logic.

## Repo map

```
cmd/worker/           Worker entry point: registers workflows, activities, error interceptor
cmd/seed/             Demo data seeder
services/search/      HTTP API; analytics/ holds one plugin per metric
workflows/            etl.go routes by integration.source; launchdarkly/, github/
activities/           shared.go (save/get), launchdarkly/, github/, error_classification.go
models/               Canonical structs, provider raw types, deterministic keys (keys.go)
repository/           pgx Postgres access, one file per entity; AES-256-GCM crypto
shared/               Cross-module error types
db/migrations/        Forward-only SQL migrations (source of truth for the schema)
infra/                Prometheus, Loki, Alloy, Grafana config
docs/                 Architecture, product, standards, historical design notes
```

Each directory is its own Go module in one `go.work` workspace. `go test ./...` does not work
from the root; use the make targets.

## Commands

```bash
make test-unit   # unit tests, no dependencies (CI)
make lint        # golangci-lint v1.64.8, all modules (CI)
make build       # bin/worker, bin/search (CI)
make vuln        # govulncheck per module (CI)
make test-int    # repository integration tests; needs Docker
make docker-up   # full local stack; see LOCALSETUP.md
make migrate     # apply migrations; needs APP_DATABASE_URL
```

Run `test-unit`, `lint` and `build` before opening a PR. When you add a module, add it to
`go.work`, `MODULE_PATHS`/`MODULE_DIRS` in the Makefile, and the lint args in
`.github/workflows/ci.yml`.

## Invariants (never break these)

1. **Tenant isolation.** Every query on a multi-tenant table filters by `org_id` first. The org
   comes only from the verified JWT (`organizationId` claim), never from request input.
2. **Idempotent writes.** Every write is `INSERT ... ON CONFLICT` on a deterministic UUID v5 key
   (`models/keys.go`). Every key includes `org_id`. Re-running any step must be safe.
3. **Raw first.** Save each raw provider response to `source_records` (append-only) before
   normalizing. Canonical rows keep `source_record_key`.
4. **Cursor after success.** Advance `integrations.last_synced_at` only after the whole window
   is saved.
5. **Source time.** `occurred_at` is the provider's timestamp, never ingestion time.
6. **Activities are the unit of retry.** No retry loops in application code. Long-term retry
   for enrichment lives in `pull_request_enrichments` by design.
7. **Workflows are deterministic.** Use `workflow.Now`, `workflow.Go`, `workflow.Sleep`; no I/O,
   no raw goroutines, no `time.Now` in workflow code.
8. **Don't wrap Temporal errors in workflows.** Return `Future.Get` errors and Temporal
   constructors (`NewContinueAsNewError`, `NewNonRetryableApplicationError`) unchanged.
   Activities return normal wrapped errors; `activities/error_classification.go` decides
   retryability.
9. **Never log secrets** (API keys, tokens, private keys) or full request/response bodies.
   Emails only at DEBUG.

## Code conventions

- **Logging:** zerolog, structured JSON. No `fmt.Println`, `log.Printf` or `slog` in service code.
  (The worker still uses `log`; don't add more, and migrating it is welcome.)
- **Errors:** wrap at package boundaries: `fmt.Errorf("SaveChangelog: %w", err)`. Log once,
  where the error is handled.
- **SQL:** `pgxpool` only; never `SELECT *`; batch with `= ANY($1)`; `timestamptz` always;
  enums as `text`. New schema changes go in a new numbered migration; never edit a merged one.
- **HTTP:** never `http.DefaultClient` or `DefaultServeMux`; always set timeouts.
- **Dependencies:** inject via constructors; no package-level mutable state or side-effecting `init()`.
- **Interfaces:** define them in the consuming package; keep them small.
- **Comments:** explain why, not what. Every exported symbol has a doc comment ending in a period.
- **Metrics:** `datagill_{service}_{noun}_{unit}_{type}`; never use unbounded labels such as
  `org_id` or `flag_key`.

Details and rationale: [`docs/engineering-standards.md`](docs/engineering-standards.md).

## Common tasks

**Add a flag or code provider** (e.g. PostHog): `models/<provider>.go` (raw types),
`activities/<provider>/` (client, extract, normalize), `workflows/<provider>/` (extract
workflow), one `case` in `workflows/etl.go`, registration in `cmd/worker/main.go`, and the
provider in `providersHandler`. No changes to existing providers. The `/add-provider` command
walks through it.

**Add an analytics metric:** one file in `services/search/analytics/` implementing `Plugin`,
registered in `register.go`, plus its repository query (org-scoped) and tests. No handler or
router changes.

**Add a migration:** next number in `db/migrations/`, forward-only. See `/new-migration`.

## Decisions and open questions

- Don't make a decision that [`docs/product.md`](docs/product.md#open-questions) lists as open.
  Ask, or propose it in the PR.
- If a change alters the architecture, update [`docs/architecture.md`](docs/architecture.md)
  in the same PR and add a line under "Decision history".
- When docs and code disagree, the code wins. Fix the doc.

## Git and docs

- Never push to `main`. Branch as `<initials>/<short-description>` and open a PR; CI must pass.
- Commit messages: `type(scope): summary`, e.g. `fix(github): retry 403 rate limits`.
- File names: UPPERCASE only for standard root files (`README.md`, `LICENSE`,
  `CONTRIBUTING.md`, `SECURITY.md`, `AGENTS.md`, `CLAUDE.md`); everything in `docs/` is
  lowercase-kebab-case (`docs/architecture.md`).
