# GitHub E2E Handoff

Status: paused for usage limits on 2026-05-03.

## Done

- `main` updated after GitHub workflow PR merge.
- Docker base stack started locally:
  - `app-postgres`
  - `temporal-postgres`
  - `temporal`
  - `temporal-ui`
- App DB migrated from `004` through:
  - `005_github_integrations`
  - `006_pull_requests`
- Found `.env` has required GitHub App keys.
- Started core branch: `fix/github-e2e-wiring`.
- Started UI branch: `feat/github-connect-flow`.

## Core Changes Started

- `docker-compose.yml`
  - Pass GitHub App env vars into `search`.
  - Pass `GITHUB_APP_ID` and `GITHUB_APP_PRIVATE_KEY_B64` into `worker`.
- `cmd/worker/go.mod` / `cmd/worker/go.sum`
  - `go mod tidy` added missing `github.com/golang-jwt/jwt/v5` checksum.
  - Needed because Docker worker build failed from clean module cache.

## Backend Build Blocker Found

`docker compose up -d --build search worker` failed before frontend work:

```text
activities/github/client.go imports github.com/golang-jwt/jwt/v5
cmd/worker/go.sum was missing the module checksum
```

Likely next check:

```sh
docker compose up -d --build search worker
```

## Frontend Work Left

Repo: `../datagill-ui`

Need wire GitHub connect flow into existing integration UI:

- Existing provider exists: `apps/app/src/lib/providers.ts` has `github`.
- Existing modal only supports API key flow:
  - `apps/app/src/components/shared/integration-modal.tsx`
- Existing entry points:
  - `apps/app/src/app/(app)/(main)/settings/integrations/page.tsx`
  - `apps/app/src/app/(app)/onboarding/step-2/page.tsx`

Minimal approach:

- For `provider.id === "github"`, skip API-key modal.
- Call backend:

```http
POST /v1/auth/github
Authorization: Bearer <BetterAuth JWT>
```

- Expect response with install/auth URL.
- Redirect browser to that URL.
- GitHub callback returns to backend `GET /v1/auth/github`.
- Backend should create integration and redirect to `FRONTEND_URL`.
- UI should invalidate `["integrations"]` after return if needed.

## E2E Checklist

1. Rebuild/start backend:
   - `docker compose up -d --build search worker`
2. Start UI auth DB/dev server if not already running.
3. Sign in locally.
4. Go to Settings > Integrations.
5. Click GitHub Connect.
6. Complete GitHub App install/authorization.
7. Verify DB:
   - `github_installations` row exists.
   - `integrations` row exists with `source = github`.
8. Verify Temporal:
   - schedule exists for integration.
   - `github-extract-<integration_key>` workflow starts.
9. Verify data:
   - `source_records.source_type in ('pr_search', 'pr_detail')`
   - `pull_requests` has canonical rows.

## Notes

- Do not amend or force push. User wants reviewable new commits.
- UI repo has AGENTS rule to ask for mock before new layout. This can avoid new layout by reusing current integration surfaces.
