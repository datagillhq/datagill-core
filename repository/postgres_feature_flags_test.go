//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/datagill/datagill-core/models"
)

func TestSaveFeatureFlag(t *testing.T) {
	ctx := context.Background()
	repo, err := NewPostgresRepository(ctx, testConnString, testEncryptionKey)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgKey := setupTestOrg(ctx, t, pool)
	integrationKey := setupTestIntegration(ctx, t, pool, orgKey)

	// timestamptz stores microseconds; truncate so values read back compare equal.
	now := time.Now().UTC().Truncate(time.Microsecond)
	flag := models.FeatureFlag{
		Key:             models.ComputeFlagKey(orgKey, models.SourceLaunchDarkly, "test-project", "my-flag"),
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
				Project:  models.FlagProject{ID: "test-project"},
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
	var createdAt time.Time
	var updatedAt time.Time
	err = pool.QueryRow(ctx, "SELECT state, created_at, updated_at FROM feature_flags WHERE key = $1", flag.Key).Scan(&state, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("failed to query feature flag: %v", err)
	}
	if state != string(models.FlagStateOn) {
		t.Errorf("expected state 'on', got %q", state)
	}
	if !createdAt.Equal(now) {
		t.Errorf("expected created_at %s, got %s", now, createdAt)
	}

	// Update (upsert)
	flag.State = models.FlagStateOff
	flag.CreatedAt = now.Add(-24 * time.Hour)
	flag.UpdatedAt = now.Add(time.Hour)
	if err := repo.SaveFeatureFlag(ctx, flag); err != nil {
		t.Fatalf("SaveFeatureFlag upsert failed: %v", err)
	}

	// Verify update
	err = pool.QueryRow(ctx, "SELECT state, created_at, updated_at FROM feature_flags WHERE key = $1", flag.Key).Scan(&state, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("failed to query feature flag after update: %v", err)
	}
	if state != string(models.FlagStateOff) {
		t.Errorf("expected state 'off' after update, got %q", state)
	}
	if !createdAt.Equal(now) {
		t.Errorf("expected created_at to remain %s, got %s", now, createdAt)
	}
	if !updatedAt.Equal(flag.UpdatedAt) {
		t.Errorf("expected updated_at to change to %s, got %s", flag.UpdatedAt, updatedAt)
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
