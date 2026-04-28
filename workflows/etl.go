// Package workflows contains Temporal workflows for the ETL worker.
// Workflows orchestrate the extraction, transformation, and loading of feature flag data.
package workflows

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/datagill/datagill-core/activities"
	"github.com/datagill/datagill-core/models"
	ldworkflows "github.com/datagill/datagill-core/workflows/launchdarkly"
)

const getIntegrationTimeout = 10 * time.Second

// ETLWorkflowInput is the input for FeatureFlagETLWorkflow.
type ETLWorkflowInput struct {
	OrgID          uuid.UUID
	IntegrationKey uuid.UUID
}

// FeatureFlagETLWorkflow is the top-level ETL workflow for one integration.
// It fetches the integration record, then routes to the provider-specific extract workflow.
// Fired by a Temporal schedule once per poll interval per integration.
func FeatureFlagETLWorkflow(ctx workflow.Context, input ETLWorkflowInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: getIntegrationTimeout,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var integration models.Integration
	if err := workflow.ExecuteActivity(ctx, (*activities.SharedActivities).GetIntegration, input.OrgID, input.IntegrationKey).Get(ctx, &integration); err != nil {
		return fmt.Errorf("FeatureFlagETLWorkflow: get integration: %w", err)
	}
	if integration.State != models.IntegrationStateActive {
		return nil
	}

	switch integration.Source {
	case models.SourceLaunchDarkly:
		return runLaunchDarklyExtract(ctx, integration)
	default:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("unsupported source: %s", integration.Source),
			"UnsupportedSource",
			nil,
		)
	}
}

func runLaunchDarklyExtract(ctx workflow.Context, integration models.Integration) error {
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: fmt.Sprintf("ld-extract-%s", integration.Key),
	})
	return workflow.ExecuteChildWorkflow(childCtx, ldworkflows.LaunchDarklyExtractWorkflow, ldworkflows.ExtractInput{
		Integration: integration,
	}).Get(ctx, nil)
}
