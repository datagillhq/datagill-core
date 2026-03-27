package launchdarkly_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	"github.com/datagill/datagill-core/activities"
	ldactivities "github.com/datagill/datagill-core/activities/launchdarkly"
	"github.com/datagill/datagill-core/models"
	ldworkflows "github.com/datagill/datagill-core/workflows/launchdarkly"
)

func TestChangelogTransformWorkflow(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	entry := models.LDAuditEntry{
		ID:   "audit-1",
		Kind: "flag",
		Date: time.Now().UnixMilli(),
		Accesses: []models.LDAccess{
			{Action: "updateOn", Resource: "proj/p:env/prod:flag/my-flag"},
		},
	}
	integrationKey := uuid.New()
	orgID := uuid.New()
	expectedChangelog := models.FeatureFlagChangelog{
		Key:       uuid.New(),
		OrgID:     orgID,
		EventType: models.EventTypeToggled,
		State:     models.RecordStateActive,
	}

	// OnActivity args: receiver, ctx, input (method expression includes receiver as arg 0)
	env.OnActivity((*ldactivities.Activities).NormalizeChangelog, mock.Anything, mock.Anything, mock.Anything).
		Return(expectedChangelog, nil)
	env.OnActivity((*activities.SharedActivities).SaveChangelog, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(ldworkflows.LaunchDarklyChangelogTransformWorkflow, ldworkflows.ChangelogTransformInput{
		Entry:          entry,
		IntegrationKey: integrationKey,
		OrgID:          orgID,
		ProjectKey:     "my-project",
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ChangelogTransformWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}

func TestFlagTransformWorkflow(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	flag := models.LDFlagDetail{
		Key:  "my-flag",
		Name: "My Flag",
		Kind: "boolean",
	}
	sourceKey := uuid.New()
	integrationKey := uuid.New()
	orgID := uuid.New()
	expectedFlag := models.FeatureFlag{
		Key:   uuid.New(),
		OrgID: orgID,
		State: models.FlagStateOff,
	}

	env.OnActivity((*ldactivities.Activities).NormalizeFlagDetail, mock.Anything, mock.Anything, mock.Anything).
		Return(expectedFlag, nil)
	env.OnActivity((*activities.SharedActivities).SaveFeatureFlag, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(ldworkflows.LaunchDarklyFlagTransformWorkflow, ldworkflows.FlagTransformInput{
		Flag:            flag,
		SourceRecordKey: sourceKey,
		IntegrationKey:  integrationKey,
		OrgID:           orgID,
		ProjectKey:      "my-project",
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("FlagTransformWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}
