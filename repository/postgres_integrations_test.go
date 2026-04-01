//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/datagill/datagill-core/models"
)

func TestEnsureOrg(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString)
	defer repo.Close()

	orgID := uuid.New()
	// First call creates the row.
	if err := repo.EnsureOrg(ctx, orgID, "ext-"+orgID.String()[:8]); err != nil {
		t.Fatalf("EnsureOrg failed: %v", err)
	}
	// Second call is a no-op — must not error.
	if err := repo.EnsureOrg(ctx, orgID, "ext-"+orgID.String()[:8]); err != nil {
		t.Fatalf("EnsureOrg (idempotent) failed: %v", err)
	}
}

func TestCreateIntegration(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	i := models.Integration{
		OrgID:  orgID,
		Source: models.SourceLaunchDarkly,
		APIKey: "api-key-123",
		Config: models.IntegrationConfig{EnvironmentKey: "production"},
	}

	created, err := repo.CreateIntegration(ctx, i)
	if err != nil {
		t.Fatalf("CreateIntegration failed: %v", err)
	}
	if created.Key == uuid.Nil {
		t.Error("expected non-nil key")
	}
	if created.State != models.IntegrationStateActive {
		t.Errorf("expected state active, got %s", created.State)
	}
	if created.Config.EnvironmentKey != "production" {
		t.Errorf("expected environment_key production, got %s", created.Config.EnvironmentKey)
	}
}

func TestGetIntegration(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	key := setupTestIntegration(ctx, t, pool, orgID)

	got, err := repo.GetIntegration(ctx, orgID, key)
	if err != nil {
		t.Fatalf("GetIntegration failed: %v", err)
	}
	if got.Key != key {
		t.Errorf("expected key %s, got %s", key, got.Key)
	}
}

func TestGetIntegration_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	_, err := repo.GetIntegration(ctx, orgID, uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetIntegration_WrongOrg(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	key := setupTestIntegration(ctx, t, pool, orgID)
	otherOrg := setupTestOrg(ctx, t, pool)

	_, err := repo.GetIntegration(ctx, otherOrg, key)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for wrong org, got %v", err)
	}
}

func TestListIntegrations_ActiveAndPaused(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	activeKey := setupTestIntegration(ctx, t, pool, orgID)

	// Insert a paused integration.
	pausedKey := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, '{}', 'paused', NOW(), NOW())
	`, pausedKey, orgID, models.SourceLaunchDarkly, "paused-key")
	if err != nil {
		t.Fatalf("insert paused integration: %v", err)
	}

	// Insert a deleted integration — should not appear.
	_, err = pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, '{}', 'deleted', NOW(), NOW())
	`, uuid.New(), orgID, models.SourceLaunchDarkly, "deleted-key")
	if err != nil {
		t.Fatalf("insert deleted integration: %v", err)
	}

	list, err := repo.ListIntegrations(ctx, orgID)
	if err != nil {
		t.Fatalf("ListIntegrations failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 integrations, got %d", len(list))
	}
	keys := map[uuid.UUID]bool{list[0].Key: true, list[1].Key: true}
	if !keys[activeKey] || !keys[pausedKey] {
		t.Errorf("expected active and paused keys, got %v", keys)
	}
}

func TestUpdateIntegration(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	key := setupTestIntegration(ctx, t, pool, orgID)

	newState := models.IntegrationStatePaused
	updated, err := repo.UpdateIntegration(ctx, orgID, key, models.IntegrationPatch{State: &newState})
	if err != nil {
		t.Fatalf("UpdateIntegration failed: %v", err)
	}
	if updated.State != models.IntegrationStatePaused {
		t.Errorf("expected state paused, got %s", updated.State)
	}
}

func TestUpdateIntegration_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	_, err := repo.UpdateIntegration(ctx, orgID, uuid.New(), models.IntegrationPatch{})
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSoftDeleteIntegration(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	key := setupTestIntegration(ctx, t, pool, orgID)

	deleted, err := repo.SoftDeleteIntegration(ctx, orgID, key)
	if err != nil {
		t.Fatalf("SoftDeleteIntegration failed: %v", err)
	}
	if deleted.State != models.IntegrationStateDeleted {
		t.Errorf("expected state deleted, got %s", deleted.State)
	}
	// Should not appear in GetIntegration after deletion.
	_, err = repo.GetIntegration(ctx, orgID, key)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after soft delete, got %v", err)
	}
}
