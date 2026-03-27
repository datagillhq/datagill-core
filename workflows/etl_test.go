package workflows_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/datagill/datagill-core/activities"
	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/workflows"
	ldworkflows "github.com/datagill/datagill-core/workflows/launchdarkly"
)

// sharedActs is a zero-value instance used only to obtain bound method references for mocking.
// The real implementation is never called because OnActivity intercepts all calls.
var sharedActs = &activities.SharedActivities{}

func TestFeatureFlagETLWorkflow_RoutesLaunchDarkly(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	integrationKey := uuid.New()
	integration := models.Integration{
		Key:    integrationKey,
		OrgID:  uuid.New(),
		Source: models.SourceLaunchDarkly,
		Config: models.IntegrationConfig{ProjectKey: "my-project"},
	}

	// Use bound method value so the testsuite does not need to deserialize the receiver.
	env.OnActivity(sharedActs.GetIntegration, mock.Anything, mock.Anything).Return(integration, nil)
	env.OnWorkflow(ldworkflows.LaunchDarklyExtractWorkflow, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(workflows.FeatureFlagETLWorkflow, integrationKey)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("FeatureFlagETLWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}

func TestFeatureFlagETLWorkflow_UnsupportedSource(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	integrationKey := uuid.New()
	integration := models.Integration{
		Key:    integrationKey,
		OrgID:  uuid.New(),
		Source: "unknown-provider",
	}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, mock.Anything).Return(integration, nil)

	env.ExecuteWorkflow(workflows.FeatureFlagETLWorkflow, integrationKey)

	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("expected error for unsupported source")
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Errorf("expected non-retryable ApplicationError, got: %v", err)
	}
}
