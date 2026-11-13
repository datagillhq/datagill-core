# Search Service

HTTP API for the DataGill UI. Go + Chi, listens on `:8082` by default (`PORT`).

It verifies JWTs, manages integrations, creates Temporal schedules when one is connected,
and serves analytics. It contains no sync logic; that runs in the worker (`cmd/worker`).

## Endpoints

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `GET` | `/healthz` | none | Liveness |
| `GET` | `/metrics` | none | Prometheus scrape |
| `GET` | `/v1/auth/github` | signed state | GitHub App install callback |
| `POST` | `/v1/auth/github` | JWT | Start GitHub App install, returns the install URL |
| `GET` | `/v1/integrations/providers` | JWT | Providers that can be connected (`launchdarkly`, `github`) |
| `POST` | `/v1/integrations` | JWT | Connect an API-key integration (LaunchDarkly) |
| `GET` | `/v1/integrations` | JWT | List the org's integrations |
| `PUT` | `/v1/integrations/{key}` | JWT | Update API key or state (pause / resume) |
| `DELETE` | `/v1/integrations/{key}` | JWT | Soft-delete an integration |
| `GET` | `/v1/analytics?metric=...` | JWT | `launched_wins`, `top_teams`, `velocity`, `audit_log`, `pull_requests` |

The org comes only from the verified JWT's `organizationId` claim (signing keys are fetched
from `AUTH_URL/.well-known/jwks.json` at startup). See [`docs/api/analytics.md`](../../docs/api/analytics.md) and
[`docs/api/integrations.md`](../../docs/api/integrations.md) for request and response shapes.

## Run locally

Required env vars: `APP_DATABASE_URL`, `AUTH_URL`, `TEMPORAL_ADDRESS`, `ENCRYPTION_KEY`.
The service exits on startup if `AUTH_URL` is unreachable, so start `datagill-ui` (separate
repo) first.

With Docker, from the repo root:

```bash
cp .env.example .env   # then set ENCRYPTION_KEY
make docker-up
```

With `go run`, after starting the infrastructure containers (see `LOCALSETUP.md`):

```bash
set -a && source .env && set +a
TEMPORAL_ADDRESS=localhost:7233 go run ./services/search
curl http://localhost:8082/healthz
```
