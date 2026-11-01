package activities

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
)

// IntegrationNotFoundErrorType is the Temporal error type GetIntegration returns when the
// integration no longer exists (for example, it was deleted). Workflows treat it as a
// signal to stop, not as a failure.
const IntegrationNotFoundErrorType = "IntegrationNotFound"

type sharedRepository interface {
	SaveSourceRecord(ctx context.Context, r models.SourceRecord) error
	SaveFeatureFlag(ctx context.Context, f models.FeatureFlag) error
	SaveChangelog(ctx context.Context, c models.FeatureFlagChangelog) error
	GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
}

// SharedActivities wraps the repository for Temporal activity registration.
// All save operations are idempotent — safe to retry on failure.
type SharedActivities struct {
	repo sharedRepository
}

// NewSharedActivities creates a new SharedActivities instance.
func NewSharedActivities(repo sharedRepository) *SharedActivities {
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

// GetIntegration returns the integration for the given org and key.
// A deleted integration returns a non-retryable IntegrationNotFoundErrorType error, since
// retrying will not bring it back.
func (a *SharedActivities) GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error) {
	integration, err := a.repo.GetIntegration(ctx, orgID, key)
	if errors.Is(err, repository.ErrNotFound) {
		return models.Integration{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("integration %s not found", key), IntegrationNotFoundErrorType, err)
	}
	if err != nil {
		return models.Integration{}, fmt.Errorf("get integration: %w", err)
	}
	return integration, nil
}

// IsIntegrationNotFound reports whether an error returned to a workflow by GetIntegration
// means the integration no longer exists.
func IsIntegrationNotFound(err error) bool {
	var appErr *temporal.ApplicationError
	return errors.As(err, &appErr) && appErr.Type() == IntegrationNotFoundErrorType
}
