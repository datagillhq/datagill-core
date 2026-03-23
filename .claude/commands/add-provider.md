Implement a new feature flag provider for DataGill. The provider name is: $ARGUMENTS

Read AGENTS.md in full before writing a single line of code. All decisions must trace back to it.

---

## What you are building

A new ETL provider integration. Adding a provider means creating exactly these files — nothing more:

```
models/{provider}.go
activities/{provider}.go
workflows/{provider}_extract.go
workflows/{provider}_transform.go
```

Plus two small edits to existing files:
- One routing case in `workflows/etl.go`
- One registration block in `cmd/worker/main.go`

Zero changes to any other existing file. If you find yourself editing `services/search/` or `repository/`, stop — something is wrong.

---

## Step 1 — Raw types (`models/{provider}.go`)

Define Go structs that mirror the provider's API responses exactly. No business logic here.

Required types:
- `{Provider}AuditEntry` — one entry from the provider's audit log / activity feed API
- `{Provider}FlagDetail` — full flag state from the provider's flag detail API

Rules:
- Use `json:"..."` tags matching the provider's actual field names
- All fields optional (`*string`, `*bool`, etc.) unless the provider guarantees presence
- Add a comment above each struct linking to the provider's API docs page

Write unit tests in `models/{provider}_test.go`:
- Unmarshal a real JSON fixture (save it under `testdata/{provider}_audit_entry.json` and `testdata/{provider}_flag_detail.json`)
- Assert key fields are populated correctly
- Assert missing optional fields are nil, not panicking

---

## Step 2 — Activities (`activities/{provider}.go`)

Five functions required. All accept `context.Context` as first argument.

**Extract activities** (call the provider API):

```go
func (a *{Provider}Activities) ListAuditLogs(ctx context.Context, integrationID uuid.UUID, before time.Time) ([]{Provider}AuditEntry, error)
func (a *{Provider}Activities) GetFlagDetail(ctx context.Context, projectKey, flagKey string) ({Provider}FlagDetail, error)
```

Requirements for both:
- Exponential backoff on 429 and 5xx (respect `Retry-After` header if present)
- 10s timeout per HTTP request (set on the `http.Client`, not via context)
- Save raw response to `source_records` via `SaveSourceRecord` before returning
- Deterministic `source_record_key` via `models.ComputeSourceRecordKey`

**Normalize activities** (pure transformations — no I/O):

```go
func Normalize{Provider}Changelog(ctx context.Context, sourceRecordKey uuid.UUID, raw {Provider}AuditEntry) (models.FeatureFlagChangelog, error)
func Normalize{Provider}FlagDetail(ctx context.Context, sourceRecordKey uuid.UUID, raw {Provider}FlagDetail) (models.FeatureFlag, error)
```

Requirements:
- No DB calls, no HTTP calls — pure input → output
- Map provider-specific event types to `models.EventType` — document the full mapping as a code comment
- Detect rollback: flag returned to previous state within 30-minute window (query last changelog entry)
- Detect automated change: use whatever token/service-account field the provider exposes
- Populate all fields of `models.FeatureFlagData` and `models.ChangelogData` that the provider supports
- Fields the provider does not expose: omit (leave as zero value / nil)

Write unit tests in `activities/{provider}_test.go`:
- Table-driven tests for both normalize functions
- Cover: standard change, automated change, rollback, missing actor, missing environment
- No external dependencies — pure function tests run with `go test -short`

---

## Step 3 — Extract workflow (`workflows/{provider}_extract.go`)

```go
func {Provider}ExtractWorkflow(ctx workflow.Context, integration models.Integration) error
```

Implementation must:
1. Call `ListAuditLogs` activity with current cursor from workflow state (initial cursor = `time.Now().Add(-backfill_days)`)
2. Collect unique flag keys from all audit entries
3. Call `GetFlagDetail` for each unique flag key **in parallel** using `workflow.Go` goroutines
4. Fan out: for each audit entry → spawn `{Provider}ChangelogTransformWorkflow` (fire-and-forget + ack)
5. Fan out: for each unique flag → spawn `{Provider}FeatureFlagTransformWorkflow` (fire-and-forget + ack)
6. Update cursor to earliest timestamp in current batch
7. Call `workflow.Continue-As-New` after 100 pages to keep history lean

Fire-and-forget + ack pattern (required for both fan-outs):
```go
childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
    ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
})
future := workflow.ExecuteChildWorkflow(childCtx, ChildWorkflow, arg)
var childExec workflow.Execution
if err := future.GetChildWorkflowExecution().Get(ctx, &childExec); err != nil {
    return fmt.Errorf("child workflow failed to start: %w", err)
}
// do NOT call future.Get() — we only wait for start, not completion
```

Write workflow unit tests using `testsuite.WorkflowTestSuite`:
- Mock all activities
- Test: normal pagination, Continue-As-New triggers at page 100, fan-out fires correct number of children
- Test: activity error is surfaced (not silently swallowed)

---

## Step 4 — Transform workflows (`workflows/{provider}_transform.go`)

Two workflows in one file:

```go
func {Provider}ChangelogTransformWorkflow(ctx workflow.Context, sourceRecordKey uuid.UUID, raw {Provider}AuditEntry) error
func {Provider}FeatureFlagTransformWorkflow(ctx workflow.Context, sourceRecordKey uuid.UUID, raw {Provider}FlagDetail) error
```

Each calls exactly two activities in sequence:
1. `Normalize{Provider}Changelog` / `Normalize{Provider}FlagDetail` — transform
2. `SaveChangelog` / `SaveFeatureFlag` (shared activities from `activities/shared.go`) — persist

No other logic. No branching. No additional activities.

Write workflow unit tests for both.

---

## Step 5 — Wire into ETL router (`workflows/etl.go`)

Add one case to the existing `switch integration.Source` block:

```go
case models.Source{Provider}:
    return workflow.ExecuteChildWorkflow(ctx, {Provider}ExtractWorkflow, integration).Get(ctx, nil)
```

---

## Step 6 — Register with worker (`cmd/worker/main.go`)

Add to the existing registration block:
```go
// Activities
w.RegisterActivity(&activities.{Provider}Activities{...})

// Workflows
w.RegisterWorkflow({Provider}ExtractWorkflow)
w.RegisterWorkflow({Provider}ChangelogTransformWorkflow)
w.RegisterWorkflow({Provider}FeatureFlagTransformWorkflow)
```

---

## Step 7 — Integration test

File: `activities/{provider}_integration_test.go` (build tag: `//go:build integration`)

- Use `testcontainers-go` to spin up Postgres and run migrations
- Use a real Temporal dev server (`temporaltest.NewServer`)
- Seed one integration row with a test API key (use env var `{PROVIDER}_TEST_API_KEY` — skip test if absent)
- Execute `{Provider}ExtractWorkflow` with a short backfill window (1 day)
- Assert: rows written to `source_records`, `feature_flags`, `feature_flag_changelog`
- Assert idempotency: run the same workflow twice, row counts must not increase

---

## Definition of done

- [ ] `go build ./...` passes
- [ ] `golangci-lint run ./...` passes with zero warnings
- [ ] `go test -short ./...` passes (unit tests only)
- [ ] All normalize functions have table-driven unit tests covering at least 5 scenarios each
- [ ] All workflows have testsuite unit tests
- [ ] Integration test exists and is tagged `//go:build integration`
- [ ] `models.Source{Provider}` added to the Source enum in `models/integration.go`
- [ ] Provider added to AGENTS.md Section 9 (Repo Structure) under "Adding a new provider"
- [ ] No changes outside the files listed in this guide
