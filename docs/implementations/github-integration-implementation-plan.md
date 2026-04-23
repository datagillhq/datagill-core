# GitHub Integration Implementation Plan

> Status: Ready for implementation planning.
> Companion to [github-integration.md](./github-integration.md).

This document translates the approved GitHub integration design into concrete repo work. It is intentionally scoped to the current codebase, which already has LaunchDarkly ETL, integration management endpoints in the Search service, and Temporal schedule wiring.

## Goals

V1 goal:

- connect a GitHub App installation from the Search service
- persist installation-scoped GitHub integrations
- schedule GitHub ETL like existing integrations
- ingest merged pull requests into raw + canonical storage
- keep scope to base PR ingestion only

Explicitly out of scope for V1:

- completed reviewers
- co-authors
- review summary
- permission drift automation
- webhook ingestion
- diff content ingestion

## Current Codebase Constraints

The current repo is not GitHub-ready yet. These are the main constraints the implementation plan must absorb:

- `models.Source` only supports LaunchDarkly, Unleash, and Flagsmith.
- `models.IntegrationConfig` is LaunchDarkly-shaped (`environment_key`) and cannot yet represent GitHub App installation metadata cleanly.
- Search integration creation assumes API-key validation before insert.
- Temporal schedule management only supports create; pause/delete do not update schedules.
- ETL routing in [workflows/etl.go](/Users/kuyoungshin/Coding/datagill-core/workflows/etl.go:1) only dispatches LaunchDarkly.
- Repository and canonical models have no `pull_requests` persistence surface yet.
- Live code still has `integrations.last_synced_at`, which conflicts with the current architecture decision that sync cursor belongs in Temporal workflow state.

## Recommended Delivery Order

Implement in five slices. Each slice should land cleanly and keep `main` releasable.

### Slice 1: Integration and schedule plumbing

Goal: let Search create and manage a GitHub integration row without any ETL yet.

Files likely touched:

- [models/integration.go](/Users/kuyoungshin/Coding/datagill-core/models/integration.go:1)
- [services/search/handlers_integrations.go](/Users/kuyoungshin/Coding/datagill-core/services/search/handlers_integrations.go:1)
- [services/search/router.go](/Users/kuyoungshin/Coding/datagill-core/services/search/router.go:1)
- [services/search/config.go](/Users/kuyoungshin/Coding/datagill-core/services/search/config.go:1)
- [services/search/app.go](/Users/kuyoungshin/Coding/datagill-core/services/search/app.go:1)
- [services/search/scheduler.go](/Users/kuyoungshin/Coding/datagill-core/services/search/scheduler.go:1)
- [repository/postgres_integrations.go](/Users/kuyoungshin/Coding/datagill-core/repository/postgres_integrations.go:1)

Tasks:

1. Add `models.SourceGitHub`.
2. Expand `IntegrationConfig` to support GitHub installation metadata:
   - `installation_id`
   - `account_login`
   - `account_type`
   - `repository_selection`
   - keep `environment_key` optional for LaunchDarkly compatibility
3. Update integration duplicate logic:
   - LaunchDarkly stays one per provider per org for now, matching current API behavior
   - GitHub uniqueness should use installation identity, not provider-only duplicate checks
   - this temporary inconsistency is intentional and should be revisited only if integration uniqueness is redesigned across all providers
4. Extend scheduler interface beyond `Create`:
   - `Pause`
   - `Resume`
   - `Delete`
5. Update integration update/delete handlers so state changes and deletes also update the Temporal schedule.

Notes:

- This slice should not yet perform GitHub App installation flow.
- It should only make the integration model and schedule lifecycle GitHub-capable.

### Slice 2: GitHub App onboarding in Search

Goal: own the browser-based GitHub App install callback in the Search service.

Files likely touched:

- [services/search/router.go](/Users/kuyoungshin/Coding/datagill-core/services/search/router.go:1)
- [services/search/handlers_integrations.go](/Users/kuyoungshin/Coding/datagill-core/services/search/handlers_integrations.go:1)
- new `services/search/github_app.go`
- new `services/search/github_app_test.go`

Tasks:

1. Add Search config for GitHub App credentials and callback setup:
   - `GITHUB_APP_ID`
   - `GITHUB_APP_CLIENT_ID`
   - `GITHUB_APP_CLIENT_SECRET` only if needed for callback verification flow
   - `GITHUB_APP_PRIVATE_KEY` or `GITHUB_APP_PRIVATE_KEY_B64`
   - `GITHUB_APP_SETUP_URL` or callback path config if required
2. Add a browser-start endpoint or return install URL from the integrations API.
3. Add GitHub App callback handler owned by Search.
4. In the callback handler:
   - verify installation context using GitHub App credentials
   - resolve installation metadata
   - upsert or create the GitHub integration row
   - create or resume the Temporal schedule
5. Decide callback idempotency behavior:
   - repeated callback for same installation updates config, does not create duplicates

Open protocol work to resolve before coding this slice:

- whether GitHub sends enough installation context directly for DataGill, or whether Search must exchange and fetch installation details afterward
- exact callback route shape used by the frontend

Recommendation:

- keep the callback server-side only
- do not persist GitHub installation access tokens in Postgres
- do not route callback logic through the worker

### Slice 3: Schema and model groundwork for pull requests

Goal: create the storage surface before implementing ETL.

Files likely touched:

- new migration for `pull_requests`
- [models/canonical.go](/Users/kuyoungshin/Coding/datagill-core/models/canonical.go:1) or new `models/github.go`
- [repository/repository.go](/Users/kuyoungshin/Coding/datagill-core/repository/repository.go:1)
- new `repository/postgres_pull_requests.go`
- new `repository/postgres_pull_requests_test.go`

Tasks:

1. Add `pull_requests` table with deterministic key on `org_id|github|pull_request.id`.
2. Add canonical PR model structs.
3. Add repository method:
   - `SavePullRequest`
4. Decide whether `pull_requests` shares any generic interfaces with feature flags.

Recommendation:

- do not try to force pull requests into existing feature-flag canonical structs
- add PR-native canonical types even if some fields look similar

Important cleanup:

- remove or deprecate `integrations.last_synced_at` in favor of Temporal workflow cursor state before GitHub ETL lands
- otherwise GitHub implementation will encode the wrong sync ownership model from day one

### Slice 4: GitHub ETL worker path

Goal: make worker ingest merged PRs for one GitHub integration.

Files likely touched:

- new `models/github.go`
- new `activities/github/activities.go`
- new `activities/github/client.go`
- new `activities/github/search_pull_requests.go`
- new `activities/github/get_pull_request_detail.go`
- new `activities/github/normalize_pull_request.go`
- new `workflows/github/extract.go`
- [workflows/etl.go](/Users/kuyoungshin/Coding/datagill-core/workflows/etl.go:1)
- [cmd/worker/main.go](/Users/kuyoungshin/Coding/datagill-core/cmd/worker/main.go:1)
- [activities/shared.go](/Users/kuyoungshin/Coding/datagill-core/activities/shared.go:1)

Tasks:

1. Add GitHub App auth client in activities:
   - sign JWT as the GitHub App
   - exchange for installation token using `installation_id`
   - use installation token only inside activity execution
2. Implement `SearchPullRequests` activity:
   - query merged PRs with search API
   - sequential pagination
   - save raw `pr_search` source records if still desired
3. Implement `GetPullRequestDetail` activity:
   - fetch full PR payload for each discovered PR
   - save `pr_detail` source record
4. Implement `NormalizePullRequest`.
5. Add `SavePullRequest` shared activity or a PR-specific shared activity.
6. Route `FeatureFlagETLWorkflow` to a new GitHub extract workflow.
7. Register GitHub activities and workflows in the worker.

V1 workflow behavior:

- hardcode PR detail concurrency to `10`
- rely on Temporal retries for GitHub rate-limit handling
- fail the run if search results are incomplete beyond supported bounds

Recommendation:

- do not implement fallback repository enumeration in the first code slice unless search behavior proves insufficient in testing
- but keep the workflow shape modular enough to add it without rewriting the extract path

### Slice 5: Hardening and end-to-end verification

Goal: prove the integration works end to end in local dev.

Tasks:

1. Add unit tests for:
   - Search callback handler
   - integration create/update/delete behavior with schedule lifecycle
   - GitHub App token generation logic
   - GitHub PR normalization
   - ETL workflow routing
2. Add repository integration tests for `pull_requests`.
3. Add workflow tests for GitHub extract orchestration.
4. Add at least one local manual verification run using Docker Compose and a real GitHub App installation in a dev account.

## API and UX Changes

The Search service integration API is currently API-key based. GitHub changes that shape.

Recommended API additions:

- `GET /v1/integrations/providers` should include `github`
- add a GitHub connect/init endpoint or include provider metadata in providers response
- add a callback endpoint outside JWT-protected org routes if GitHub cannot call it with user JWT context

Recommendation:

- do not overload the existing `POST /v1/integrations` API with fake API-key semantics for GitHub
- instead add a separate GitHub connect flow that creates the integration only after GitHub App installation succeeds

## Schedule Lifecycle Work

This is required for GitHub but should be fixed generically for all integrations.

Current gap:

- create integration creates a Temporal schedule
- pause/delete integration do not pause/delete that schedule

Implementation requirement:

- pausing an integration pauses the Temporal schedule
- reactivating resumes it
- deleting removes the schedule

If this is not fixed before GitHub onboarding, uninstall behavior will be incorrect.

## Testing Plan

Minimum test matrix for the first GitHub implementation:

- Search handler tests for callback success, duplicate installation, invalid installation context
- Repository tests for GitHub integration config round-tripping
- Repository tests for `pull_requests` upsert idempotency
- Activity tests for GitHub App token generation and PR normalization
- Workflow tests for provider routing and GitHub extract fan-out
- One manual end-to-end test with real GitHub App installation

## Recommended PR Breakdown

Keep this work in reviewable chunks rather than one giant branch.

1. PR 1: integration model + schedule lifecycle cleanup
2. PR 2: Search-side GitHub App onboarding flow
3. PR 3: pull request schema + repository layer
4. PR 4: worker-side GitHub extract and normalize path
5. PR 5: hardening, tests, and docs cleanup

## Risks to Watch

- Search callback route design may be more awkward than expected because JWT-scoped org APIs are not the same as GitHub callback surfaces.
- The current `IntegrationConfig` type may become too provider-specific again if GitHub fields are bolted on carelessly.
- Search API visibility under installation tokens should be verified early with a real GitHub App installation.
- Schedule lifecycle bugs will create confusing “deleted but still syncing” behavior if not fixed first.
- `last_synced_at` drift should not leak into new GitHub ETL code.

## First Spike Recommendation

Before building the full ingestion path, do one short spike:

1. stand up a DataGill GitHub App in a dev GitHub account
2. wire a minimal callback endpoint in Search
3. store a GitHub integration row from a real installation
4. mint an installation token server-side
5. call one low-risk GitHub API endpoint successfully with that token

That spike will validate the hardest new boundary early: GitHub App setup plus installation-token auth in the existing Search/worker split.
