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

1. Install Go 1.26.
2. From `datagill-core`, run `go run ./services/search/...`.
3. Open `http://localhost:8082/healthz`.

## Docker run

1. Install Docker Desktop.
2. From `datagill-core`, run `docker compose up search --build`.
3. Open `http://localhost:8082/healthz`.
