package activities

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
)

// SharedActivities wraps the repository for Temporal activity registration.
// All save operations are idempotent — safe to retry on failure.
type SharedActivities struct {
	repo repository.Repository
}

// NewSharedActivities creates a new SharedActivities instance.
func NewSharedActivities(repo repository.Repository) *SharedActivities {
	return &SharedActivities{repo: repo}
}

// SaveSourceRecord persists a raw provider API response.
// Idempotent: upserts on source_records.key.
func (a *SharedActivities) SaveSourceRecord(ctx context.Context, r models.SourceRecord) error {
	if err := a.repo.SaveSourceRecord(ctx, r); err != nil {
		return fmt.Errorf("save source record: %w", err)
	}
	return nil
}

// SaveFeatureFlag upserts a normalized feature flag.
// Idempotent: upserts on feature_flags.key.
func (a *SharedActivities) SaveFeatureFlag(ctx context.Context, f models.FeatureFlag) error {
	if err := a.repo.SaveFeatureFlag(ctx, f); err != nil {
		return fmt.Errorf("save feature flag: %w", err)
	}
	return nil
}

// SaveChangelog upserts a normalized changelog entry.
// Idempotent: upserts on feature_flag_changelog.key.
func (a *SharedActivities) SaveChangelog(ctx context.Context, c models.FeatureFlagChangelog) error {
	if err := a.repo.SaveChangelog(ctx, c); err != nil {
		return fmt.Errorf("save changelog: %w", err)
	}
	return nil
}

// GetIntegration returns the integration for the given key.
func (a *SharedActivities) GetIntegration(ctx context.Context, key uuid.UUID) (models.Integration, error) {
	integration, err := a.repo.GetIntegration(ctx, key)
	if err != nil {
		return models.Integration{}, fmt.Errorf("get integration: %w", err)
	}
	return integration, nil
}
