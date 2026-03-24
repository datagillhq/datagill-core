//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/datagill/datagill-core/models"
)

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
