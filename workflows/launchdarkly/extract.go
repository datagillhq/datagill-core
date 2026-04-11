package launchdarkly

import (
	"go.temporal.io/sdk/workflow"

	"github.com/datagill/datagill-core/models"
)

// ExtractInput is the input for LaunchDarklyExtractWorkflow.
type ExtractInput struct {
	Integration models.Integration
}

// LaunchDarklyExtractWorkflow orchestrates the full extract phase for one LaunchDarkly integration.
// TODO: implement in a follow-up PR.
func LaunchDarklyExtractWorkflow(_ workflow.Context, _ ExtractInput) error {
	return nil
}
