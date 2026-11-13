.PHONY: build lint test test-unit test-int vuln docker-up docker-down migrate

# Module import paths in the go.work workspace that have code to test and lint.
# Keep in sync with go.work.
MODULE_PATHS := \
	github.com/datagill/datagill-core/models/... \
	github.com/datagill/datagill-core/repository/... \
	github.com/datagill/datagill-core/shared/... \
	github.com/datagill/datagill-core/activities/... \
	github.com/datagill/datagill-core/workflows/... \
	github.com/datagill/datagill-core/worker/... \
	github.com/datagill/datagill-core/seed/... \
	github.com/datagill/datagill-core/services/search/...

# Module directories, for tools that do not understand go.work (golangci-lint
# patterns, govulncheck).
MODULE_DIRS := models repository shared activities workflows cmd/worker cmd/seed services/search

# Build the two binaries (worker, search) into ./bin.
build:
	go build -o bin/worker github.com/datagill/datagill-core/worker
	go build -o bin/search github.com/datagill/datagill-core/services/search

# Run golangci-lint across all modules.
# Install: https://golangci-lint.run/usage/install/ (CI pins v1.64.8)
lint:
	golangci-lint run $(addprefix ./,$(addsuffix /...,$(MODULE_DIRS)))

# Run all tests (unit + integration). Integration tests need Docker.
test: test-unit test-int

# Run unit tests only (no external dependencies required).
test-unit:
	go test -short $(MODULE_PATHS)

# Run integration tests only (requires Docker for testcontainers).
test-int:
	go test -tags=integration $(MODULE_PATHS)

# Scan every module for known vulnerabilities.
# Install: go install golang.org/x/vuln/cmd/govulncheck@latest
# govulncheck does not support go.work, so it runs once per module.
vuln:
	@set -e; for dir in $(MODULE_DIRS); do \
		echo "==> govulncheck $$dir"; \
		(cd $$dir && govulncheck ./...); \
	done

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
