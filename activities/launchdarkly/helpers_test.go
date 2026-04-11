package launchdarkly

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
)

// mockRepository implements repository.Repository for testing.
type mockRepository struct {
	sourceRecords []models.SourceRecord
	featureFlags  []models.FeatureFlag
	changelogs    []models.FeatureFlagChangelog
	integrations  map[uuid.UUID]models.Integration
}

func newMockRepository() *mockRepository {
	return &mockRepository{integrations: make(map[uuid.UUID]models.Integration)}
}

func (r *mockRepository) SaveSourceRecord(_ context.Context, rec models.SourceRecord) error {
	r.sourceRecords = append(r.sourceRecords, rec)
	return nil
}

func (r *mockRepository) SaveFeatureFlag(_ context.Context, f models.FeatureFlag) error {
	r.featureFlags = append(r.featureFlags, f)
	return nil
}

func (r *mockRepository) SaveChangelog(_ context.Context, c models.FeatureFlagChangelog) error {
	r.changelogs = append(r.changelogs, c)
	return nil
}

func (r *mockRepository) GetIntegration(_ context.Context, _ uuid.UUID, key uuid.UUID) (models.Integration, error) {
	return r.integrations[key], nil
}

func (r *mockRepository) ListIntegrations(_ context.Context, _ uuid.UUID) ([]models.Integration, error) {
	var result []models.Integration
	for _, i := range r.integrations {
		result = append(result, i)
	}
	return result, nil
}

func (r *mockRepository) CreateIntegration(_ context.Context, i models.Integration) (models.Integration, error) {
	return i, nil
}

func (r *mockRepository) UpdateIntegration(_ context.Context, _, _ uuid.UUID, _ models.IntegrationPatch) (models.Integration, error) {
	return models.Integration{}, nil
}

func (r *mockRepository) SoftDeleteIntegration(_ context.Context, _, _ uuid.UUID) (models.Integration, error) {
	return models.Integration{}, nil
}

func (r *mockRepository) CreateOrg(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

func (r *mockRepository) QueryLaunchedWins(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string) (repository.LaunchedWinsResult, error) {
	return repository.LaunchedWinsResult{}, nil
}

func (r *mockRepository) QueryTopTeams(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.TopTeamsResult, error) {
	return repository.TopTeamsResult{}, nil
}

func (r *mockRepository) QueryVelocity(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.VelocityResult, error) {
	return repository.VelocityResult{}, nil
}

func (r *mockRepository) QueryAuditLog(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string, _, _ int) (repository.AuditLogResult, error) {
	return repository.AuditLogResult{}, nil
}

func mustNewClient(t *testing.T, apiKey string, opts ...ClientOption) *Client {
	t.Helper()
	client, err := NewClient(apiKey, opts...)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return client
}
