//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/datagill/datagill-core/models"
)

func TestUpsertGitHubIntegration(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString, testEncryptionKey)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)

	first, created, err := repo.UpsertGitHubIntegration(ctx, orgID, models.IntegrationConfig{
		InstallationID: 12345,
	})
	if err != nil {
		t.Fatalf("UpsertGitHubIntegration create failed: %v", err)
	}
	if !created {
		t.Fatal("expected first upsert to create a row")
	}

	second, created, err := repo.UpsertGitHubIntegration(ctx, orgID, models.IntegrationConfig{
		InstallationID: 12345,
	})
	if err != nil {
		t.Fatalf("UpsertGitHubIntegration update failed: %v", err)
	}
	if created {
		t.Fatal("expected second upsert to update existing row")
	}
	if first.Key != second.Key {
		t.Fatalf("expected same integration key, got %s and %s", first.Key, second.Key)
	}
	if second.Config.InstallationID != 12345 {
		t.Fatalf("expected installation id to round-trip, got %d", second.Config.InstallationID)
	}
}
