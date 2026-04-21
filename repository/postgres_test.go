//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/datagill/datagill-core/models"
)

var testConnString string

// testEncryptionKey is a 32-byte AES-256 key used only in tests.
var testEncryptionKey = []byte("test-encryption-key-32-bytes!!!!")

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("datagill_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		panic("failed to start postgres container: " + err.Error())
	}

	testConnString, err = pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("failed to get connection string: " + err.Error())
	}

	// Run migrations
	if err := runMigrations(ctx, testConnString); err != nil {
		panic("failed to run migrations: " + err.Error())
	}

	code := m.Run()

	// Cleanup
	if err := pgContainer.Terminate(ctx); err != nil {
		panic("failed to terminate container: " + err.Error())
	}

	os.Exit(code)
}

func runMigrations(ctx context.Context, connString string) error {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return err
	}
	defer pool.Close()

	migrationsDir := filepath.Join("..", "db", "migrations")

	// Run migrations in order
	migrations := []string{
		"001_initialize.up.sql",
		"002_indexes.up.sql",
		"003_analytics_indexes.up.sql",
		"004_integration_last_synced_at.up.sql",
	}

	for _, mig := range migrations {
		content, err := os.ReadFile(filepath.Join(migrationsDir, mig))
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(content)); err != nil {
			return err
		}
	}

	return nil
}

func setupTestOrg(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	orgKey := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO organizations (key, external_org_id, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
	`, orgKey, "test-org-"+orgKey.String()[:8])
	if err != nil {
		t.Fatalf("failed to create test org: %v", err)
	}
	return orgKey
}

func setupTestIntegration(ctx context.Context, t *testing.T, pool *pgxpool.Pool, orgKey uuid.UUID) uuid.UUID {
	t.Helper()
	integrationKey := uuid.New()
	config := models.IntegrationConfig{EnvironmentKey: "production"}
	configJSON, _ := json.Marshal(config)

	encryptedKey, err := encryptAPIKey(testEncryptionKey, "test-api-key")
	if err != nil {
		t.Fatalf("failed to encrypt test api key: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
	`, integrationKey, orgKey, models.SourceLaunchDarkly, encryptedKey, configJSON, models.IntegrationStateActive)
	if err != nil {
		t.Fatalf("failed to create test integration: %v", err)
	}
	return integrationKey
}
