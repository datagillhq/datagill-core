package github_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	ghactivities "github.com/datagill/datagill-core/activities/github"
	"github.com/datagill/datagill-core/models"
	ghworkflows "github.com/datagill/datagill-core/workflows/github"
)

// enrichmentActivityCalls is the exact number of times each enrichment activity must run.
type enrichmentActivityCalls struct {
	list, flagKeys, claim, enrich, save, markFailed int
}

func pendingEnrichment(orgID uuid.UUID) models.PullRequestEnrichment {
	return models.PullRequestEnrichment{
		PullRequestKey: uuid.New(),
		OrgID:          orgID,
		Status:         models.PREnrichmentStatusPending,
	}
}

// runEnrichment registers catch-all mocks behind the test's own expectations, runs the
// workflow, and asserts exact call counts. The catch-alls matter: without them, a call the
// test did not expect fails only that activity (which the workflow logs and swallows),
// so the test would pass even when the workflow ran something it should not have.
func runEnrichment(t *testing.T, env *testsuite.TestWorkflowEnvironment, orgID uuid.UUID, want enrichmentActivityCalls) {
	t.Helper()
	env.OnActivity(ghActs.ListPendingEnrichments, mock.Anything, mock.Anything).Return([]models.PullRequestEnrichment{}, nil).Maybe()
	env.OnActivity(ghActs.ListFlagSourceKeys, mock.Anything, mock.Anything).Return([]string{}, nil).Maybe()
	env.OnActivity(ghActs.ClaimEnrichment, mock.Anything, mock.Anything).Return(false, nil).Maybe()
	env.OnActivity(ghActs.EnrichPullRequest, mock.Anything, mock.Anything).
		Return(models.PullRequestEnrichmentData{}, errors.New("unexpected EnrichPullRequest call")).Maybe()
	env.OnActivity(ghActs.SaveEnrichmentResult, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(ghActs.MarkEnrichmentFailed, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.ExecuteWorkflow(ghworkflows.GitHubEnrichmentWorkflow, ghworkflows.EnrichmentInput{OrgID: orgID})

	if !env.IsWorkflowCompleted() {
		t.Fatal("GitHubEnrichmentWorkflow did not complete")
	}
	// A nil error (rather than ContinueAsNew) proves the workflow runs once and lets the
	// per-org schedule own the cadence.
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("GitHubEnrichmentWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
	env.AssertActivityNumberOfCalls(t, "ListPendingEnrichments", want.list)
	env.AssertActivityNumberOfCalls(t, "ListFlagSourceKeys", want.flagKeys)
	env.AssertActivityNumberOfCalls(t, "ClaimEnrichment", want.claim)
	env.AssertActivityNumberOfCalls(t, "EnrichPullRequest", want.enrich)
	env.AssertActivityNumberOfCalls(t, "SaveEnrichmentResult", want.save)
	env.AssertActivityNumberOfCalls(t, "MarkEnrichmentFailed", want.markFailed)
}

// rowInput is the org-scoped activity input the workflow sends for one enrichment row.
func rowInput(row models.PullRequestEnrichment) ghactivities.EnrichmentRowInput {
	return ghactivities.EnrichmentRowInput{OrgID: row.OrgID, PullRequestKey: row.PullRequestKey}
}

func TestGitHubEnrichmentWorkflow_EmptyBatchIsNoOp(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	orgID := uuid.New()

	env.OnActivity(ghActs.ListPendingEnrichments, mock.Anything, ghactivities.ListPendingEnrichmentsInput{OrgID: orgID, Limit: 100}).
		Return([]models.PullRequestEnrichment{}, nil).Once()

	runEnrichment(t, env, orgID, enrichmentActivityCalls{list: 1})
}

func TestGitHubEnrichmentWorkflow_SavesSuccessfulEnrichment(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	orgID := uuid.New()
	row := pendingEnrichment(orgID)
	flagKeys := []string{"price_test"}
	data := models.PullRequestEnrichmentData{
		FeatureFlags: models.PullRequestFeatureFlagRefs{Added: []string{uuid.NewString()}},
	}

	env.OnActivity(ghActs.ListPendingEnrichments, mock.Anything, mock.Anything).Return([]models.PullRequestEnrichment{row}, nil).Once()
	env.OnActivity(ghActs.ListFlagSourceKeys, mock.Anything, ghactivities.ListFlagSourceKeysInput{OrgID: orgID}).Return(flagKeys, nil).Once()
	env.OnActivity(ghActs.ClaimEnrichment, mock.Anything, rowInput(row)).Return(true, nil).Once()
	env.OnActivity(ghActs.EnrichPullRequest, mock.Anything, ghactivities.EnrichPRInput{
		PullRequestKey: row.PullRequestKey,
		OrgID:          orgID,
		FlagKeys:       flagKeys,
	}).Return(data, nil).Once()
	env.OnActivity(ghActs.SaveEnrichmentResult, mock.Anything, ghactivities.SaveEnrichmentResultInput{
		OrgID:          orgID,
		PullRequestKey: row.PullRequestKey,
		Data:           data,
	}).Return(nil).Once()

	runEnrichment(t, env, orgID, enrichmentActivityCalls{list: 1, flagKeys: 1, claim: 1, enrich: 1, save: 1})
}

func TestGitHubEnrichmentWorkflow_SkipsUnclaimedRow(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	orgID := uuid.New()
	row := pendingEnrichment(orgID)

	env.OnActivity(ghActs.ListPendingEnrichments, mock.Anything, mock.Anything).Return([]models.PullRequestEnrichment{row}, nil).Once()
	env.OnActivity(ghActs.ListFlagSourceKeys, mock.Anything, mock.Anything).Return([]string{"price_test"}, nil).Once()
	env.OnActivity(ghActs.ClaimEnrichment, mock.Anything, rowInput(row)).Return(false, nil).Once()

	runEnrichment(t, env, orgID, enrichmentActivityCalls{list: 1, flagKeys: 1, claim: 1})
}

func TestGitHubEnrichmentWorkflow_MarksFailedWhenEnrichFails(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	orgID := uuid.New()
	row := pendingEnrichment(orgID)

	env.OnActivity(ghActs.ListPendingEnrichments, mock.Anything, mock.Anything).Return([]models.PullRequestEnrichment{row}, nil).Once()
	env.OnActivity(ghActs.ListFlagSourceKeys, mock.Anything, mock.Anything).Return([]string{"price_test"}, nil).Once()
	env.OnActivity(ghActs.ClaimEnrichment, mock.Anything, rowInput(row)).Return(true, nil).Once()
	env.OnActivity(ghActs.EnrichPullRequest, mock.Anything, mock.Anything).
		Return(models.PullRequestEnrichmentData{}, errors.New("github unavailable"))
	env.OnActivity(ghActs.MarkEnrichmentFailed, mock.Anything, rowInput(row)).Return(nil).Once()

	// A persistent error must stop after the bounded retry policy's 3 attempts, not retry
	// forever, so the row is handed back to the attempts cap via MarkEnrichmentFailed.
	runEnrichment(t, env, orgID, enrichmentActivityCalls{list: 1, flagKeys: 1, claim: 1, enrich: 3, markFailed: 1})
}

func TestGitHubEnrichmentWorkflow_MarksFailedWhenSaveFails(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	orgID := uuid.New()
	row := pendingEnrichment(orgID)

	env.OnActivity(ghActs.ListPendingEnrichments, mock.Anything, mock.Anything).Return([]models.PullRequestEnrichment{row}, nil).Once()
	env.OnActivity(ghActs.ListFlagSourceKeys, mock.Anything, mock.Anything).Return([]string{"price_test"}, nil).Once()
	env.OnActivity(ghActs.ClaimEnrichment, mock.Anything, rowInput(row)).Return(true, nil).Once()
	env.OnActivity(ghActs.EnrichPullRequest, mock.Anything, mock.Anything).Return(models.PullRequestEnrichmentData{}, nil).Once()
	env.OnActivity(ghActs.SaveEnrichmentResult, mock.Anything, mock.Anything).Return(errors.New("db unavailable"))
	env.OnActivity(ghActs.MarkEnrichmentFailed, mock.Anything, rowInput(row)).Return(nil).Once()

	runEnrichment(t, env, orgID, enrichmentActivityCalls{list: 1, flagKeys: 1, claim: 1, enrich: 1, save: 3, markFailed: 1})
}

func TestGitHubEnrichmentWorkflow_OneFailureDoesNotBlockOtherRows(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	orgID := uuid.New()
	failing := pendingEnrichment(orgID)
	healthy := pendingEnrichment(orgID)

	env.OnActivity(ghActs.ListPendingEnrichments, mock.Anything, mock.Anything).
		Return([]models.PullRequestEnrichment{failing, healthy}, nil).Once()
	env.OnActivity(ghActs.ListFlagSourceKeys, mock.Anything, mock.Anything).Return([]string{"price_test"}, nil).Once()
	env.OnActivity(ghActs.ClaimEnrichment, mock.Anything, mock.Anything).Return(true, nil).Twice()
	env.OnActivity(ghActs.EnrichPullRequest, mock.Anything, mock.MatchedBy(func(input ghactivities.EnrichPRInput) bool {
		return input.PullRequestKey == failing.PullRequestKey
	})).Return(models.PullRequestEnrichmentData{}, errors.New("github unavailable"))
	env.OnActivity(ghActs.EnrichPullRequest, mock.Anything, mock.MatchedBy(func(input ghactivities.EnrichPRInput) bool {
		return input.PullRequestKey == healthy.PullRequestKey
	})).Return(models.PullRequestEnrichmentData{}, nil).Once()
	env.OnActivity(ghActs.MarkEnrichmentFailed, mock.Anything, rowInput(failing)).Return(nil).Once()
	env.OnActivity(ghActs.SaveEnrichmentResult, mock.Anything, mock.MatchedBy(func(input ghactivities.SaveEnrichmentResultInput) bool {
		return input.PullRequestKey == healthy.PullRequestKey
	})).Return(nil).Once()

	runEnrichment(t, env, orgID, enrichmentActivityCalls{list: 1, flagKeys: 1, claim: 2, enrich: 4, save: 1, markFailed: 1})
}
