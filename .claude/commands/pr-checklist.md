Run the pre-PR checklist for the current branch. Check every item below. Fix any failures before opening the PR. Do not open the PR until all items pass.

---

## 1. Automated checks (run these first)

```bash
make lint
make test-unit
go build ./...
```

All three must exit 0. Fix every lint warning and test failure — do not suppress with `//nolint` unless you add an inline comment explaining why.

---

## 2. Code correctness

For every file changed, verify:

- [ ] **No ignored errors.** Every `err` return is either handled or explicitly discarded with a comment. Search: `grep -n "_ ="` and `grep -n ", _)"` in changed files.
- [ ] **No `http.DefaultClient`.** Outbound HTTP calls use a client with explicit timeouts.
- [ ] **No hardcoded secrets.** API keys, tokens, passwords read from environment variables only.
- [ ] **Context propagation.** Every function that does I/O (DB, HTTP, Temporal activity) accepts `ctx context.Context` as its first argument and passes it through.
- [ ] **Errors wrapped at package boundaries.** `fmt.Errorf("FunctionName: %w", err)` — not bare `return err`.

---

## 3. Database

For every DB query added or modified:

- [ ] **org_id is the first predicate** on every query against a multi-tenant table (`feature_flags`, `feature_flag_changelog`, `source_records`, `integrations`). No exception.
- [ ] **No `SELECT *`.** Every query names its columns explicitly.
- [ ] **No N+1 queries.** Fetching a list and then querying per item in a loop is a bug. Use `WHERE key = ANY($1::uuid[])` batch fetch or a JOIN.
- [ ] **Writes are idempotent.** Every `INSERT` uses `ON CONFLICT (key) DO UPDATE`. Running the same operation twice must produce the same final state.
- [ ] **New migrations follow the convention.** File named `{NNN}_{description}.sql`, sequential number, includes both up and down logic as comments, tested with `make migrate`.

---

## 4. Temporal workflows and activities

For every workflow or activity added or modified:

- [ ] **No raw `go` goroutines in workflows.** Use `workflow.Go` exclusively.
- [ ] **No I/O in workflows.** Workflows are pure orchestration — all I/O happens in activities.
- [ ] **No retry logic in activities.** Activities return errors; Temporal retries them. No `for { retry }` loops.
- [ ] **Fire-and-forget uses the ack pattern.** Child workflows spawned fire-and-forget call `GetChildWorkflowExecution().Get()` to confirm start. `ParentClosePolicy` is `ABANDON`.
- [ ] **Continue-As-New** is called after 100 pages in any paginating extract workflow.

---

## 5. Observability

- [ ] **New endpoints have Prometheus metrics.** Request counter and duration histogram registered and incremented.
- [ ] **New workflows/activities have metrics.** Execution counter (with status label) and duration histogram.
- [ ] **No unbounded label values.** Verify no `org_id`, `flag_key`, `user_id`, or `request_id` is used as a Prometheus label value.
- [ ] **Errors are logged at ERROR level** at the point they are handled (not at every layer). Log includes enough context to diagnose the problem (relevant IDs, operation name).
- [ ] **No secrets in log output.** Grep changed files for any `log` call that references API key, token, password, or credential fields.

---

## 6. Tests

- [ ] **Unit tests exist** for every new pure function (normalize functions, key computation, query parsers). Table-driven with at least 3 scenarios per function.
- [ ] **Handler unit tests exist** for every new HTTP handler using `httptest`.
- [ ] **Integration tests exist** (or are explicitly deferred with a TODO + ticket) for any new repository method or end-to-end path.
- [ ] **Integration tests are tagged** `//go:build integration` — they must not run with `go test -short`.
- [ ] **No mocked DB in integration tests.** Use `testcontainers-go` with a real Postgres instance.

---

## 7. Clean Code

- [ ] **No function exceeds ~40 lines.** If it does, break it up.
- [ ] **No magic numbers.** Literal values that have meaning are named constants.
- [ ] **Comments explain WHY.** Scan all new comments — if any just restate what the code does, delete them or replace with the reasoning.
- [ ] **No new package-level mutable `var`.** Dependencies are injected via constructors.
- [ ] **Naming is explicit.** No unexplained abbreviations. No stutter (e.g. `models.FeatureFlagModel`).

---

## 8. AGENTS.md compliance

- [ ] **No contradiction with AGENTS.md.** If this PR changes behavior that conflicts with AGENTS.md or docs/architecture.md, update them in the same PR and add a line under "Decision history" in docs/architecture.md.
- [ ] **Open questions not resolved.** If the PR depends on an open question in docs/product.md, resolve it there before merging.
- [ ] **New provider or schema change documented.** If this PR adds a provider or changes data-model conventions, docs/architecture.md (Data model) and the AGENTS.md repo map / common tasks are updated.

---

## 9. PR description

The PR description must include:
- Which phase and task from `IMPLEMENTATION_PLAN.md` this covers (e.g., "Phase 2A.4 — normalize activities")
- What was changed and why (not just what — the commit messages say what)
- Any decisions made that are not obvious from the code

---

## If anything fails

Fix it before opening the PR. Do not open a PR with known lint failures, failing tests, or standards violations and add "will fix in follow-up" — fix it now. The only exception is explicitly deferred integration tests, which must have a TODO comment with a reference.
