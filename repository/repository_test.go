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
	config := models.IntegrationConfig{
		ProjectKey:          "test-project",
		EnvironmentKey:      "production",
		PollIntervalMinutes: 5,
		BackfillDays:        30,
	}
	configJSON, _ := json.Marshal(config)

	_, err := pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
	`, integrationKey, orgKey, models.SourceLaunchDarkly, "test-api-key", configJSON, models.IntegrationStateActive)
	if err != nil {
		t.Fatalf("failed to create test integration: %v", err)
	}
	return integrationKey
}

func TestSaveSourceRecord(t *testing.T) {
	ctx := context.Background()
	repo, err := NewPostgresRepository(ctx, testConnString)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgKey := setupTestOrg(ctx, t, pool)
	integrationKey := setupTestIntegration(ctx, t, pool, orgKey)

	rec := models.SourceRecord{
		Key:           models.ComputeSourceRecordKey(models.SourceLaunchDarkly, models.SourceTypeAuditLog, "audit-123"),
		IntegrationID: integrationKey,
		OrgID:         orgKey,
		SourceType:    models.SourceTypeAuditLog,
		Data:          json.RawMessage(`{"id": "audit-123", "action": "flag.updated"}`),
		CreatedAt:     time.Now().UTC(),
	}

	// First insert
	if err := repo.SaveSourceRecord(ctx, rec); err != nil {
		t.Fatalf("SaveSourceRecord failed: %v", err)
	}

	// Verify idempotent - second insert should not error
	if err := repo.SaveSourceRecord(ctx, rec); err != nil {
		t.Fatalf("SaveSourceRecord idempotent call failed: %v", err)
	}

	// Verify record exists
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM source_records WHERE key = $1", rec.Key).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count source records: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 source record, got %d", count)
	}
}

func TestSaveFeatureFlag(t *testing.T) {
	ctx := context.Background()
	repo, err := NewPostgresRepository(ctx, testConnString)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgKey := setupTestOrg(ctx, t, pool)
	integrationKey := setupTestIntegration(ctx, t, pool, orgKey)

	now := time.Now().UTC()
	flag := models.FeatureFlag{
		Key:             models.ComputeFlagKey(models.SourceLaunchDarkly, "test-project", "my-flag"),
		SourceRecordKey: uuid.New(),
		IntegrationID:   integrationKey,
		OrgID:           orgKey,
		State:           models.FlagStateOn,
		Data: models.FeatureFlagData{
			Name:        "My Flag",
			Description: "A test flag",
			Source: models.FlagSource{
				Key:      "my-flag",
				Provider: models.SourceLaunchDarkly,
				Project:  "test-project",
			},
			Lifecycle: models.FlagLifecycle{
				Purpose: models.LifecyclePurposeTemporary,
				State:   models.LifecycleStateActive,
			},
			Environments: map[string]models.FlagEnvironment{
				"production": {Enabled: true},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	// First insert
	if err := repo.SaveFeatureFlag(ctx, flag); err != nil {
		t.Fatalf("SaveFeatureFlag failed: %v", err)
	}

	// Verify record exists with correct state
	var state string
	err = pool.QueryRow(ctx, "SELECT state FROM feature_flags WHERE key = $1", flag.Key).Scan(&state)
	if err != nil {
		t.Fatalf("failed to query feature flag: %v", err)
	}
	if state != string(models.FlagStateOn) {
		t.Errorf("expected state 'on', got %q", state)
	}

	// Update (upsert)
	flag.State = models.FlagStateOff
	flag.UpdatedAt = time.Now().UTC()
	if err := repo.SaveFeatureFlag(ctx, flag); err != nil {
		t.Fatalf("SaveFeatureFlag upsert failed: %v", err)
	}

	// Verify update
	err = pool.QueryRow(ctx, "SELECT state FROM feature_flags WHERE key = $1", flag.Key).Scan(&state)
	if err != nil {
		t.Fatalf("failed to query feature flag after update: %v", err)
	}
	if state != string(models.FlagStateOff) {
		t.Errorf("expected state 'off' after update, got %q", state)
	}

	// Verify only one record
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM feature_flags WHERE key = $1", flag.Key).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count feature flags: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 feature flag, got %d", count)
	}
}

func TestSaveChangelog(t *testing.T) {
	ctx := context.Background()
	repo, err := NewPostgresRepository(ctx, testConnString)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgKey := setupTestOrg(ctx, t, pool)
	integrationKey := setupTestIntegration(ctx, t, pool, orgKey)

	now := time.Now().UTC()
	changelog := models.FeatureFlagChangelog{
		Key:             models.ComputeChangelogKey(models.SourceLaunchDarkly, "test-project", "event-456"),
		SourceRecordKey: uuid.New(),
		FeatureFlagKey:  models.ComputeFlagKey(models.SourceLaunchDarkly, "test-project", "my-flag"),
		IntegrationID:   integrationKey,
		OrgID:           orgKey,
		EventType:       models.EventTypeToggled,
		State:           models.FlagStateOn,
		OccurredAt:      now,
		Data: models.ChangelogData{
			FlagName:    "my-flag",
			Description: "Turned on the flag",
			Actor: models.ChangelogActor{
				Email:       "user@example.com",
				IsAutomated: false,
			},
			Environment:   "production",
			Project:       "test-project",
			IsRollback:    false,
			PreviousState: "off",
			NewState:      "on",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	// First insert
	if err := repo.SaveChangelog(ctx, changelog); err != nil {
		t.Fatalf("SaveChangelog failed: %v", err)
	}

	// Verify idempotent
	if err := repo.SaveChangelog(ctx, changelog); err != nil {
		t.Fatalf("SaveChangelog idempotent call failed: %v", err)
	}

	// Verify only one record
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM feature_flag_changelog WHERE key = $1", changelog.Key).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count changelog: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 changelog entry, got %d", count)
	}
}

func TestGetIntegration(t *testing.T) {
	ctx := context.Background()
	repo, err := NewPostgresRepository(ctx, testConnString)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgKey := setupTestOrg(ctx, t, pool)
	integrationKey := setupTestIntegration(ctx, t, pool, orgKey)

	// Fetch integration
	integration, err := repo.GetIntegration(ctx, integrationKey)
	if err != nil {
		t.Fatalf("GetIntegration failed: %v", err)
	}

	// Verify fields
	if integration.Key != integrationKey {
		t.Errorf("expected key %s, got %s", integrationKey, integration.Key)
	}
	if integration.OrgID != orgKey {
		t.Errorf("expected org_id %s, got %s", orgKey, integration.OrgID)
	}
	if integration.Source != models.SourceLaunchDarkly {
		t.Errorf("expected source launchdarkly, got %s", integration.Source)
	}
	if integration.State != models.IntegrationStateActive {
		t.Errorf("expected state active, got %s", integration.State)
	}
	if integration.Config.ProjectKey != "test-project" {
		t.Errorf("expected project_key test-project, got %s", integration.Config.ProjectKey)
	}
}

func TestGetIntegration_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, err := NewPostgresRepository(ctx, testConnString)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	// Query for non-existent integration
	_, err = repo.GetIntegration(ctx, uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListIntegrations(t *testing.T) {
	ctx := context.Background()
	repo, err := NewPostgresRepository(ctx, testConnString)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgKey := setupTestOrg(ctx, t, pool)

	// Create an active integration
	activeKey := setupTestIntegration(ctx, t, pool, orgKey)

	// Create a paused integration
	pausedKey := uuid.New()
	config := models.IntegrationConfig{
		ProjectKey:          "paused-project",
		EnvironmentKey:      "production",
		PollIntervalMinutes: 5,
		BackfillDays:        30,
	}
	configJSON, _ := json.Marshal(config)
	_, err = pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
	`, pausedKey, orgKey, models.SourceLaunchDarkly, "paused-api-key", configJSON, models.IntegrationStatePaused)
	if err != nil {
		t.Fatalf("failed to create paused integration: %v", err)
	}

	// List integrations - should only return active
	integrations, err := repo.ListIntegrations(ctx, orgKey)
	if err != nil {
		t.Fatalf("ListIntegrations failed: %v", err)
	}

	if len(integrations) != 1 {
		t.Fatalf("expected 1 integration, got %d", len(integrations))
	}

	if integrations[0].Key != activeKey {
		t.Errorf("expected active integration key %s, got %s", activeKey, integrations[0].Key)
	}
}
