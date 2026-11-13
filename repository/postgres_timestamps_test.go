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

// LaunchDarkly activities leave CreatedAt and UpdatedAt unset. The save methods pass both
// as explicit parameters, which skips the columns' DEFAULT NOW(), so a zero time must be
// replaced rather than stored as 0001-01-01.
func TestSaveMethods_ZeroTimestampsBecomeNow(t *testing.T) {
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
	before := time.Now().Add(-time.Minute)

	rec := models.SourceRecord{
		Key:           uuid.New(),
		IntegrationID: integrationKey,
		OrgID:         orgKey,
		SourceType:    models.SourceTypeAuditLog,
		Data:          []byte(`{"id":"zero-ts"}`),
	}
	if err := repo.SaveSourceRecord(ctx, rec); err != nil {
		t.Fatalf("SaveSourceRecord failed: %v", err)
	}

	changelog := models.FeatureFlagChangelog{
		Key:             uuid.New(),
		SourceRecordKey: rec.Key,
		FeatureFlagKey:  uuid.New(),
		IntegrationID:   integrationKey,
		OrgID:           orgKey,
		EventType:       models.EventTypeUpdated,
		State:           models.RecordStateActive,
		OccurredAt:      time.Now().UTC(),
	}
	if err := repo.SaveChangelog(ctx, changelog); err != nil {
		t.Fatalf("SaveChangelog failed: %v", err)
	}

	flag := models.FeatureFlag{
		Key:             uuid.New(),
		SourceRecordKey: rec.Key,
		IntegrationID:   integrationKey,
		OrgID:           orgKey,
		State:           models.FlagStateOn,
	}
	if err := repo.SaveFeatureFlag(ctx, flag); err != nil {
		t.Fatalf("SaveFeatureFlag failed: %v", err)
	}

	checks := []struct {
		name  string
		query string
		key   uuid.UUID
	}{
		{"source_records.created_at", `SELECT created_at FROM source_records WHERE key = $1`, rec.Key},
		{"feature_flag_changelog.created_at", `SELECT created_at FROM feature_flag_changelog WHERE key = $1`, changelog.Key},
		{"feature_flag_changelog.updated_at", `SELECT updated_at FROM feature_flag_changelog WHERE key = $1`, changelog.Key},
		{"feature_flags.created_at", `SELECT created_at FROM feature_flags WHERE key = $1`, flag.Key},
		{"feature_flags.updated_at", `SELECT updated_at FROM feature_flags WHERE key = $1`, flag.Key},
	}
	for _, c := range checks {
		var got time.Time
		if err := pool.QueryRow(ctx, c.query, c.key).Scan(&got); err != nil {
			t.Fatalf("%s: query failed: %v", c.name, err)
		}
		if got.Before(before) {
			t.Errorf("%s = %s, want the time of the write", c.name, got)
		}
	}
}
