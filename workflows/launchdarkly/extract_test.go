package launchdarkly_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/datagill/datagill-core/activities"
	ldactivities "github.com/datagill/datagill-core/activities/launchdarkly"
	"github.com/datagill/datagill-core/models"
	ldworkflows "github.com/datagill/datagill-core/workflows/launchdarkly"
)

func testIntegration() models.Integration {
	return models.Integration{
		Key:    uuid.New(),
		OrgID:  uuid.New(),
		Source: models.SourceLaunchDarkly,
	}
}

func TestExtractWorkflow_HappyPath(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	integration := testIntegration()
	flagKey := "my-flag"
	auditEntry := models.LDAuditEntry{
		ID:   "audit-1",
		Kind: "flag",
		Date: time.Now().UnixMilli(),
		Accesses: []models.LDAccess{
			{Action: "updateOn", Resource: "proj/my-project:env/prod:flag/" + flagKey},
		},
	}
	scrollOut := ldactivities.ScrollAuditLogsOutput{
		Entries: []models.LDAuditEntry{auditEntry},
	}
	flagDetailOut := ldactivities.GetFlagDetailOutput{
		Flag:            models.LDFlagDetail{Key: flagKey},
		SourceRecordKey: uuid.New(),
	}

	env.OnActivity((*ldactivities.Activities).ScrollAuditLogs, mock.Anything, mock.Anything, mock.Anything).
		Return(scrollOut, nil)
	env.OnActivity((*ldactivities.Activities).GetFlagDetail, mock.Anything, mock.Anything, mock.Anything).
		Return(flagDetailOut, nil)
	env.OnActivity((*ldactivities.Activities).NormalizeChangelog, mock.Anything, mock.Anything, mock.Anything).
		Return(models.FeatureFlagChangelog{Key: uuid.New(), OrgID: integration.OrgID}, nil)
	env.OnActivity((*activities.SharedActivities).SaveChangelog, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity((*ldactivities.Activities).NormalizeFlagDetail, mock.Anything, mock.Anything, mock.Anything).
		Return(models.FeatureFlag{Key: uuid.New(), OrgID: integration.OrgID}, nil)
	env.OnActivity((*activities.SharedActivities).SaveFeatureFlag, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity((*ldactivities.Activities).UpdateLastSyncedAt, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(ldworkflows.LaunchDarklyExtractWorkflow, ldworkflows.ExtractInput{
		Integration: integration,
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ExtractWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}

func TestExtractWorkflow_SkipsNonFlagEntries(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	integration := testIntegration()
	scrollOut := ldactivities.ScrollAuditLogsOutput{
		Entries: []models.LDAuditEntry{
			{ID: "audit-env", Kind: "environment", Date: time.Now().UnixMilli()},
		},
	}

	env.OnActivity((*ldactivities.Activities).ScrollAuditLogs, mock.Anything, mock.Anything, mock.Anything).
		Return(scrollOut, nil)
	env.OnActivity((*ldactivities.Activities).UpdateLastSyncedAt, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	// No transform activities — non-flag entries are skipped entirely.

	env.ExecuteWorkflow(ldworkflows.LaunchDarklyExtractWorkflow, ldworkflows.ExtractInput{
		Integration: integration,
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ExtractWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}

func TestExtractWorkflow_FlagDetailNotFoundSkipped(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	integration := testIntegration()
	flagKey := "deleted-flag"
	auditEntry := models.LDAuditEntry{
		ID:   "audit-deleted",
		Kind: "flag",
		Date: time.Now().UnixMilli(),
		Accesses: []models.LDAccess{
			{Action: "deleteFlag", Resource: "proj/my-project:env/prod:flag/" + flagKey},
		},
	}
	scrollOut := ldactivities.ScrollAuditLogsOutput{
		Entries: []models.LDAuditEntry{auditEntry},
	}

	env.OnActivity((*ldactivities.Activities).ScrollAuditLogs, mock.Anything, mock.Anything, mock.Anything).
		Return(scrollOut, nil)
	// Flag detail returns non-retryable error (deleted) — workflow must skip, not fail.
	env.OnActivity((*ldactivities.Activities).GetFlagDetail, mock.Anything, mock.Anything, mock.Anything).
		Return(ldactivities.GetFlagDetailOutput{}, temporal.NewNonRetryableApplicationError("not found", "NotFound", nil))
	// Changelog transform still runs for the deletion audit entry.
	env.OnActivity((*ldactivities.Activities).NormalizeChangelog, mock.Anything, mock.Anything, mock.Anything).
		Return(models.FeatureFlagChangelog{Key: uuid.New(), OrgID: integration.OrgID}, nil)
	env.OnActivity((*activities.SharedActivities).SaveChangelog, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity((*ldactivities.Activities).UpdateLastSyncedAt, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(ldworkflows.LaunchDarklyExtractWorkflow, ldworkflows.ExtractInput{
		Integration: integration,
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ExtractWorkflow should succeed even when flag detail is not found: %v", err)
	}
	env.AssertExpectations(t)
}

func TestExtractWorkflow_IncrementalSync(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	lastSync := time.Now().Add(-24 * time.Hour)
	integration := testIntegration()
	integration.LastSyncedAt = &lastSync

	scrollOut := ldactivities.ScrollAuditLogsOutput{Entries: nil}

	env.OnActivity((*ldactivities.Activities).ScrollAuditLogs, mock.Anything, mock.Anything, mock.Anything).
		Return(scrollOut, nil)
	env.OnActivity((*ldactivities.Activities).UpdateLastSyncedAt, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(ldworkflows.LaunchDarklyExtractWorkflow, ldworkflows.ExtractInput{
		Integration: integration,
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("Incremental sync failed: %v", err)
	}
	env.AssertExpectations(t)
}
