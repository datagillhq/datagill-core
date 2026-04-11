package launchdarkly

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/datagill/datagill-core/activities"
	ldactivities "github.com/datagill/datagill-core/activities/launchdarkly"
	"github.com/datagill/datagill-core/models"
)

const transformActivityTimeout = 30 * time.Second

// ChangelogTransformInput is the input for LaunchDarklyChangelogTransformWorkflow.
type ChangelogTransformInput struct {
	Entry          models.LDAuditEntry
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
}

// FlagTransformInput is the input for LaunchDarklyFlagTransformWorkflow.
type FlagTransformInput struct {
	Flag            models.LDFlagDetail
	SourceRecordKey uuid.UUID
	IntegrationKey  uuid.UUID
	OrgID           uuid.UUID
	ProjectKey      string
}

// LaunchDarklyChangelogTransformWorkflow normalizes an audit entry and saves the resulting changelog.
func LaunchDarklyChangelogTransformWorkflow(ctx workflow.Context, input ChangelogTransformInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: transformActivityTimeout,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var changelog models.FeatureFlagChangelog
	normalizeInput := ldactivities.NormalizeChangelogInput{
		Entry:          input.Entry,
		IntegrationKey: input.IntegrationKey,
		OrgID:          input.OrgID,
	}
	if err := workflow.ExecuteActivity(ctx, (*ldactivities.Activities).NormalizeChangelog, normalizeInput).Get(ctx, &changelog); err != nil {
		return fmt.Errorf("LaunchDarklyChangelogTransformWorkflow: normalize: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, (*activities.SharedActivities).SaveChangelog, changelog).Get(ctx, nil); err != nil {
		return fmt.Errorf("LaunchDarklyChangelogTransformWorkflow: save: %w", err)
	}

	return nil
}

// LaunchDarklyFlagTransformWorkflow normalizes a flag detail and saves the resulting feature flag.
func LaunchDarklyFlagTransformWorkflow(ctx workflow.Context, input FlagTransformInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: transformActivityTimeout,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var featureFlag models.FeatureFlag
	normalizeInput := ldactivities.NormalizeFlagDetailInput{
		Flag:            input.Flag,
		SourceRecordKey: input.SourceRecordKey,
		IntegrationKey:  input.IntegrationKey,
		OrgID:           input.OrgID,
		ProjectKey:      input.ProjectKey,
	}
	if err := workflow.ExecuteActivity(ctx, (*ldactivities.Activities).NormalizeFlagDetail, normalizeInput).Get(ctx, &featureFlag); err != nil {
		return fmt.Errorf("LaunchDarklyFlagTransformWorkflow: normalize: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, (*activities.SharedActivities).SaveFeatureFlag, featureFlag).Get(ctx, nil); err != nil {
		return fmt.Errorf("LaunchDarklyFlagTransformWorkflow: save: %w", err)
	}

	return nil
}

// isNonRetryable reports whether err is a non-retryable Temporal application error.
func isNonRetryable(err error) bool {
	var appErr *temporal.ApplicationError
	return errors.As(err, &appErr) && appErr.NonRetryable()
}
