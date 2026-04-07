# Search Service

Minimal bootstrap for the DataGill Search Service.

## Current shape

- Go + Chi HTTP service
- Read-only bootstrap endpoints
- In-memory `SearchRepository` for local UI work
- No Temporal dependency

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `POST /ping`
- `GET /api/v1/search`
- `GET /api/v1/flags`

## Local run

The search service now requires `APP_DATABASE_URL`, so `go run` will fail unless
Postgres is available and the env var is set.

Recommended path:

1. From `datagill-core`, copy `.env.example` to `.env`.
2. Start local dependencies with `make docker-up`.
3. Run `docker compose up search --build`.
4. Open `http://localhost:8082/healthz`.

Direct Go path:

1. Install Go 1.26.
2. From `datagill-core`, copy `.env.example` to `.env`.
3. Start Postgres with `make docker-up`.
4. Load env vars into your shell:

   ```bash
   set -a
   source .env
   set +a
   ```

5. Run `go run ./services/search/...`.
6. Open `http://localhost:8082/healthz`.

## Docker run

1. Install Docker Desktop.
2. From `datagill-core`, copy `.env.example` to `.env`.
3. Run `docker compose up search --build`.
4. Open `http://localhost:8082/healthz`.
