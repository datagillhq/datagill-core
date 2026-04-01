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

func TestSaveChangelog(t *testing.T) {
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

	now := time.Now().UTC()
	changelog := models.FeatureFlagChangelog{
		Key:             models.ComputeChangelogKey(orgKey, models.SourceLaunchDarkly, "test-project", "event-456"),
		SourceRecordKey: uuid.New(),
		FeatureFlagKey:  models.ComputeFlagKey(orgKey, models.SourceLaunchDarkly, "test-project", "my-flag"),
		IntegrationID:   integrationKey,
		OrgID:           orgKey,
		EventType:       models.EventTypeToggled,
		State:           models.RecordStateActive,
		OccurredAt:      now,
		Data: models.ChangelogData{
			Description: "Turned on the flag",
			Actor: models.ChangelogActor{
				Email: "user@example.com",
			},
			Source: models.FlagSource{
				Key:      "event-456",
				Provider: models.SourceLaunchDarkly,
				Project:  models.FlagProject{ID: "test-project"},
			},
			Environments: []string{"production"},
			ChangeType:   "flag_enabled",
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
