# Local Development Setup

This guide gets a fresh machine running the full DataGill stack in under 10 minutes.

## Architecture overview

| Service | Repo | Port | Notes |
|---------|------|------|-------|
| Frontend (Next.js) | `datagill-ui` | 3001 | Also serves BetterAuth |
| Backend (search API) | `datagill-core` | 8082 | Go + Chi |
| App Postgres | `datagill-core` | 5432 | Feature flag data |
| Auth Postgres | `datagill-ui` | 5433 | BetterAuth sessions/users |
| Temporal | `datagill-core` | 7233 | Workflow orchestration |
| Temporal UI | `datagill-core` | 8080 | Optional, useful for debugging |

**Start order:** frontend first — the backend fetches JWKS from BetterAuth at startup.

---

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Go | 1.26+ | https://go.dev/dl |
| Node.js | 20+ | https://nodejs.org or `nvm` |
| Docker Desktop | Any recent | https://www.docker.com/products/docker-desktop |
| golang-migrate | 4.x | `brew install golang-migrate` |

---

## 1. Frontend (`datagill-ui`)

### 1a. Environment

Copy the example env file:

```bash
cp .env.example apps/app/.env.local
```

Fill in the required values in `apps/app/.env.local`:

```bash
# Generate a secret:
openssl rand -base64 32
# Paste the output as BETTER_AUTH_SECRET
```

The other values can stay as the defaults from `.env.example` for local dev.

### 1b. Start the auth database

```bash
docker compose up -d auth-db
```

This starts a Postgres instance on port **5433** for BetterAuth. BetterAuth
auto-migrates its schema on first boot — no separate migration step needed.

### 1c. Install dependencies and start

```bash
npm install
npm run dev
```

The app is now at **http://localhost:3001**.

Create an account and an organisation through the UI before starting the backend.

---

## 2. Backend (`datagill-core`)

### 2a. Environment

Copy the example env file:

```bash
cp .env.example .env
```

Fill in the two required secrets:

```bash
# Generate a 64-char hex encryption key:
openssl rand -hex 32
# Paste as ENCRYPTION_KEY in .env
```

Set `AUTH_URL` to the BetterAuth base path served by the frontend:

```
AUTH_URL=http://localhost:3001/api/auth
```


### 2b. Start infrastructure

```bash
make docker-up
```

This brings up:
- `app-postgres` on port **5432**
- `temporal` + `temporal-postgres` on port **7233**
- `temporal-ui` on port **8080**
- Prometheus / Loki / Grafana (observability stack)

Auth is handled by BetterAuth in `datagill-ui` — there is no auth service in this repo.

### 2c. Run migrations

```bash
make migrate
```

Requires `APP_DATABASE_URL` to be set (it is, after sourcing `.env`):

```bash
set -a && source .env && set +a
make migrate
```

### 2d. Start the backend

```bash
set -a && source .env && set +a
TEMPORAL_ADDRESS=localhost:7233 go run ./services/search/...
```

> `TEMPORAL_ADDRESS` is not in `.env` because the compose network uses the
> `temporal` hostname internally. Override it to `localhost:7233` when running
> the service directly with `go run`.

The API is now at **http://localhost:8082**. Verify with:

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
# Terminal 1 — frontend
cd datagill-ui
docker compose up -d auth-db
npm run dev

# Terminal 2 — backend infra
cd datagill-core
make docker-up

# Terminal 3 — backend service
cd datagill-core
set -a && source .env && set +a
TEMPORAL_ADDRESS=localhost:7233 go run ./services/search/...
```

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
Run `make docker-up` and wait a few seconds for the healthcheck to pass before retrying.
