//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Invariant 1: the sync cursor is only written for the org that owns the integration.
func TestUpdateIntegrationLastSyncedAt_IsOrgScoped(t *testing.T) {
	ctx := context.Background()
	repo, err := NewPostgresRepository(ctx, testConnString, testEncryptionKey)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgKey := setupTestOrg(ctx, t, pool)
	otherOrg := setupTestOrg(ctx, t, pool)
	integrationKey := setupTestIntegration(ctx, t, pool, orgKey)
	syncedAt := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	lastSyncedAt := func() *time.Time {
		t.Helper()
		var got *time.Time
		if err := pool.QueryRow(ctx, `SELECT last_synced_at FROM integrations WHERE key = $1`, integrationKey).Scan(&got); err != nil {
			t.Fatalf("query last_synced_at: %v", err)
		}
		return got
	}

	if err := repo.UpdateIntegrationLastSyncedAt(ctx, otherOrg, integrationKey, syncedAt); err != nil {
		t.Fatalf("UpdateIntegrationLastSyncedAt (other org): %v", err)
	}
	if got := lastSyncedAt(); got != nil {
		t.Fatalf("another org moved the cursor to %s", got)
	}

	if err := repo.UpdateIntegrationLastSyncedAt(ctx, orgKey, integrationKey, syncedAt); err != nil {
		t.Fatalf("UpdateIntegrationLastSyncedAt: %v", err)
	}
	if got := lastSyncedAt(); got == nil || !got.Equal(syncedAt) {
		t.Errorf("last_synced_at = %v, want %s", got, syncedAt)
	}
}
