//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/datagill/datagill-core/models"
)

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
