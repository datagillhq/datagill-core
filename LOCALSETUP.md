# Local Development Setup

This guide gets `datagill-core` running locally and notes the one cross-repo dependency on `datagill-ui`.

## Dependency note

Run `datagill-ui` first, then start `datagill-core`. The backend fetches JWKS from BetterAuth on startup.

For UI setup, see [datagill-ui/LOCALSETUP.md](../datagill-ui/LOCALSETUP.md).

---

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Go | 1.26+ | https://go.dev/dl |
| Node.js | 20+ | https://nodejs.org or `nvm` |
| Docker Desktop | Any recent | https://www.docker.com/products/docker-desktop |
| golang-migrate | 4.x | `brew install golang-migrate` |

---

## Quick start

If you have already completed the one-time setup, use these commands:

```bash
# 1. Start the full backend stack in Docker
make docker-up
```

Make sure `datagill-ui` is already running before starting the backend.

---

## First-time setup

### 1. Create the local env file

```bash
cp .env.example .env
```

Fill in the required local secrets:

```bash
# Generate a 64-char hex encryption key:
openssl rand -hex 32
# Paste as ENCRYPTION_KEY in .env

# Generate a GitHub state signing secret:
openssl rand -hex 32
# Paste as GITHUB_APP_STATE_SECRET in .env
```

Set `AUTH_URL` to the BetterAuth base path served by the frontend:

```
AUTH_URL=http://localhost:3001/api/auth
```

For GitHub onboarding in Search, also set:

```
GITHUB_APP_ID=<your GitHub App ID>
GITHUB_APP_CLIENT_ID=<your GitHub App client ID>
GITHUB_APP_CLIENT_SECRET=<your GitHub App client secret>
GITHUB_APP_PRIVATE_KEY_B64=<base64-encoded PEM private key>
GITHUB_APP_SLUG=<your GitHub App slug>
GITHUB_APP_CALLBACK_URL=http://localhost:8082/v1/auth/github
GITHUB_APP_STATE_SECRET=<random secret used to sign state>
FRONTEND_URL=http://localhost:3001
```

`GITHUB_APP_CALLBACK_URL` must exactly match the callback URL configured in your GitHub App settings.

To produce `GITHUB_APP_PRIVATE_KEY_B64` from a downloaded PEM file:

```bash
base64 < path/to/github-app-private-key.pem | tr -d '\n'
```

### 2. Start the backend stack

```bash
make docker-up
```

This brings up:
- `app-postgres` on port **5432**
- `temporal` + `temporal-postgres` on port **7233**
- `temporal-ui` on port **8080**
- Prometheus / Loki / Grafana (observability stack)
- `search` on port **8082**
- `worker` for the `etl` task queue

Auth is handled by BetterAuth in `datagill-ui` — there is no auth service in this repo.

### 3. Run migrations

If you do not already have the `migrate` CLI installed:

```bash
brew install golang-migrate
```

Export `.env` first so `APP_DATABASE_URL` is available to the `Makefile`, then run:

```bash
set -a && source .env && set +a
make migrate
```

### 4. Verify the API

```bash
curl http://localhost:8082/healthz
# {"service":"search","status":"ok"}
```

---

## Quick reference

### Stopping everything

```bash
# In datagill-core:
make docker-down

# In datagill-ui:
docker compose down
```

### Re-running after a restart

Services do not auto-start. Each session:

```bash
# datagill-ui must already be running before the backend starts

# Terminal 1 — backend
cd datagill-core
make docker-up
```

---

## Optional: local Go dev mode

If you want faster edit/run/debug loops for backend code, run only infrastructure in Docker and start the API and worker locally.

### Start infrastructure only

```bash
docker compose up -d app-postgres temporal-postgres temporal temporal-ui prometheus loki alloy grafana
```

### Start the API service locally

```bash
set -a && source .env && set +a
TEMPORAL_ADDRESS=localhost:7233 go run ./services/search/...
```

### Start the worker locally

Run this in a separate terminal:

```bash
set -a && source .env && set +a
TEMPORAL_ADDRESS=localhost:7233 go run ./cmd/worker/...
```

`TEMPORAL_ADDRESS` is not in `.env` because the compose network uses the `temporal` hostname internally. Override it to `localhost:7233` when running services directly with `go run`.

---

## Troubleshooting

### `failed to fetch JWKS`
The backend cannot reach BetterAuth. Make sure:
- `datagill-ui` is running (`npm run dev` in that repo)
- `AUTH_URL` in `.env` is `http://localhost:3001/api/auth` (include the path)

### `TEMPORAL_ADDRESS is required`
Set it inline: `TEMPORAL_ADDRESS=localhost:7233 go run ./services/search/...`

### `ENCRYPTION_KEY must be a 64-character hex string`
Generate one: `openssl rand -hex 32`

### Postgres connection refused
Run the infrastructure containers and wait a few seconds for the healthchecks to pass before retrying.

### `listen tcp :8082: bind: address already in use`
If you are using local Go dev mode, do not run `make docker-up`. That target also starts the containerized `search` service on port `8082`.
