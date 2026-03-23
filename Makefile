.PHONY: build lint test test-unit test-int docker-up docker-down migrate

# Build all modules in the workspace.
build:
	go build github.com/datagill/datagill-core/worker
	go build github.com/datagill/datagill-core/services/search

# Run golangci-lint across all modules.
# Install: https://golangci-lint.run/usage/install/
lint:
	golangci-lint run ./models/... ./repository/... ./cmd/worker/... ./services/search/...

# Run all tests (unit + integration).
test:
	go test github.com/datagill/datagill-core/models/...
	go test github.com/datagill/datagill-core/repository/...
	go test github.com/datagill/datagill-core/services/search/...

# Run unit tests only (no external dependencies required).
test-unit:
	go test -short github.com/datagill/datagill-core/models/...
	go test -short github.com/datagill/datagill-core/repository/...
	go test -short github.com/datagill/datagill-core/services/search/...

# Run integration tests only (requires Docker for testcontainers).
test-int:
	go test -run Integration github.com/datagill/datagill-core/models/...
	go test -run Integration github.com/datagill/datagill-core/repository/...
	go test -run Integration github.com/datagill/datagill-core/services/search/...

# Start all infrastructure services locally.
docker-up:
	docker compose up -d

# Stop all infrastructure services.
docker-down:
	docker compose down

# Apply pending database migrations against app-postgres.
# Requires APP_DATABASE_URL to be set.
migrate:
	migrate -path db/migrations -database "$(APP_DATABASE_URL)" up
