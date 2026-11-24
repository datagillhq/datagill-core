package github_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/datagill/datagill-core/activities"
	ghactivities "github.com/datagill/datagill-core/activities/github"
	"github.com/datagill/datagill-core/models"
	ghworkflows "github.com/datagill/datagill-core/workflows/github"
)

var ghActs = &ghactivities.Activities{}
var sharedActs = &activities.SharedActivities{}

func TestGitHubExtractWorkflow_IngestsDiscoveredPullRequests(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(now)

	orgID := uuid.New()
	integrationKey := uuid.New()
	integration := models.Integration{
		Key:    integrationKey,
		OrgID:  orgID,
		Source: models.SourceGitHub,
		State:  models.IntegrationStateActive,
		// Last successful sync 30 minutes ago: this run searches the last 35 minutes.
		LastSyncedAt: timePtr(now.Add(-30 * time.Minute)),
	}
	ref := ghactivities.PullRequestRef{
		SourceKey: "987654321",
		Owner:     "acme",
		Repo:      "backend",
		Number:    42,
		Labels:    []string{"feature"},
		Assignees: []models.PullRequestAssignee{{Login: "mira", ID: 2001}},
	}
	detail := models.GitHubPullRequestDetail{ID: 123456789, Number: 42, Title: "Ship checkout"}
	sourceRecordKey := uuid.New()
	pr := models.PullRequest{Key: uuid.New(), OrgID: orgID, IntegrationID: integrationKey}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, orgID, integrationKey).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.IntegrationKey == integrationKey &&
			input.OrgID == orgID &&
			input.From.Equal(now.Add(-35*time.Minute)) &&
			input.To.Equal(now) &&
			input.Page == 1 &&
			input.PerPage == 100
	})).Return(ghactivities.SearchPullRequestsOutput{Refs: []ghactivities.PullRequestRef{ref}}, nil).Once()
	env.OnActivity(ghActs.GetPullRequestDetail, mock.Anything, mock.MatchedBy(func(input ghactivities.GetPullRequestDetailInput) bool {
		return input.IntegrationKey == integrationKey &&
			input.OrgID == orgID &&
			input.Ref.Owner == "acme" &&
			input.Ref.Repo == "backend" &&
			input.Ref.Number == 42
	})).Return(ghactivities.GetPullRequestDetailOutput{Ref: ref, Detail: detail, SourceRecordKey: sourceRecordKey}, nil).Once()
	env.OnActivity(ghActs.NormalizePullRequest, mock.Anything, mock.MatchedBy(func(input ghactivities.NormalizePullRequestInput) bool {
		return input.IntegrationKey == integrationKey &&
			input.OrgID == orgID &&
			input.SourceRecordKey == sourceRecordKey &&
			input.Detail.ID == detail.ID &&
			len(input.Ref.Labels) == 1 &&
			input.Ref.Labels[0] == "feature" &&
			len(input.Ref.Assignees) == 1 &&
			input.Ref.Assignees[0].Login == "mira"
	})).Return(pr, nil).Once()
	env.OnActivity(ghActs.SavePullRequest, mock.Anything, pr).Return(nil).Once()

	expectSyncedThrough(env, orgID, integrationKey, now)

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("GitHubExtractWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}

func TestGitHubExtractWorkflow_SplitsLargeSearchWindow(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(now)

	orgID := uuid.New()
	integrationKey := uuid.New()
	integration := models.Integration{
		Key:    integrationKey,
		OrgID:  orgID,
		Source: models.SourceGitHub,
		State:  models.IntegrationStateActive,
		// Last successful sync 30 minutes ago: this run searches the last 35 minutes.
		LastSyncedAt: timePtr(now.Add(-30 * time.Minute)),
	}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, orgID, integrationKey).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.From.Equal(now.Add(-35*time.Minute)) && input.To.Equal(now)
	})).Return(ghactivities.SearchPullRequestsOutput{TotalCount: 901}, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.From.Equal(now.Add(-35*time.Minute)) && input.To.Equal(now.Add(-17*time.Minute-30*time.Second))
	})).Return(ghactivities.SearchPullRequestsOutput{}, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.From.Equal(now.Add(-17*time.Minute-30*time.Second)) && input.To.Equal(now)
	})).Return(ghactivities.SearchPullRequestsOutput{}, nil).Once()

	expectSyncedThrough(env, orgID, integrationKey, now)

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("GitHubExtractWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}

func TestGitHubExtractWorkflow_ReturnsSearchError(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(now)

	orgID := uuid.New()
	integrationKey := uuid.New()
	integration := models.Integration{
		Key:    integrationKey,
		OrgID:  orgID,
		Source: models.SourceGitHub,
		State:  models.IntegrationStateActive,
		// Last successful sync 30 minutes ago: this run searches the last 35 minutes.
		LastSyncedAt: timePtr(now.Add(-30 * time.Minute)),
	}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, orgID, integrationKey).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.From.Equal(now.Add(-35*time.Minute)) && input.To.Equal(now)
	})).Return(ghactivities.SearchPullRequestsOutput{}, errors.New("github search failed")).Once()

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow error")
	}
	env.AssertExpectations(t)
}

func TestGitHubExtractWorkflow_UsesLastSearchedThroughWithOverlap(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(now)

	orgID := uuid.New()
	integrationKey := uuid.New()
	lastSearchedThrough := now.Add(-30 * time.Minute)
	integration := models.Integration{
		Key:    integrationKey,
		OrgID:  orgID,
		Source: models.SourceGitHub,
		State:  models.IntegrationStateActive,
	}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, orgID, integrationKey).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.From.Equal(lastSearchedThrough.Add(-5*time.Minute)) && input.To.Equal(now)
	})).Return(ghactivities.SearchPullRequestsOutput{}, nil).Once()

	expectSyncedThrough(env, orgID, integrationKey, now)

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{
		Integration:         integration,
		LastSearchedThrough: &lastSearchedThrough,
		RunOnce:             true,
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("GitHubExtractWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}

func TestGitHubExtractWorkflow_ReturnsSaveError(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(now)

	orgID := uuid.New()
	integrationKey := uuid.New()
	integration := models.Integration{
		Key:    integrationKey,
		OrgID:  orgID,
		Source: models.SourceGitHub,
		State:  models.IntegrationStateActive,
	}
	ref := ghactivities.PullRequestRef{Owner: "acme", Repo: "backend", Number: 42}
	detail := models.GitHubPullRequestDetail{ID: 123456789, Number: 42}
	sourceRecordKey := uuid.New()
	pr := models.PullRequest{Key: uuid.New(), OrgID: orgID, IntegrationID: integrationKey}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, orgID, integrationKey).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.Anything).Return(ghactivities.SearchPullRequestsOutput{Refs: []ghactivities.PullRequestRef{ref}}, nil).Once()
	env.OnActivity(ghActs.GetPullRequestDetail, mock.Anything, mock.Anything).Return(ghactivities.GetPullRequestDetailOutput{Ref: ref, Detail: detail, SourceRecordKey: sourceRecordKey}, nil).Once()
	env.OnActivity(ghActs.NormalizePullRequest, mock.Anything, mock.Anything).Return(pr, nil).Once()
	env.OnActivity(ghActs.SavePullRequest, mock.Anything, pr).Return(errors.New("db unavailable")).Once()

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow error")
	}
	env.AssertExpectations(t)
}

func TestGitHubExtractWorkflow_SkipsPausedIntegration(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	orgID := uuid.New()
	integrationKey := uuid.New()
	inputIntegration := models.Integration{
		Key:    integrationKey,
		OrgID:  orgID,
		Source: models.SourceGitHub,
		State:  models.IntegrationStateActive,
	}
	currentIntegration := inputIntegration
	currentIntegration.State = models.IntegrationStatePaused

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, orgID, integrationKey).Return(currentIntegration, nil).Once()

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: inputIntegration, RunOnce: true})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("GitHubExtractWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}

// If the integration is deleted while the extract runs, it stops without calling GitHub.
func TestGitHubExtractWorkflow_SkipsDeletedIntegration(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub, State: models.IntegrationStateActive}
	env.OnActivity(sharedActs.GetIntegration, mock.Anything, integration.OrgID, integration.Key).
		Return(models.Integration{}, temporal.NewNonRetryableApplicationError("integration not found", activities.IntegrationNotFoundErrorType, nil)).Once()

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("expected deleted integration to be skipped, got %v", err)
	}
	env.AssertExpectations(t)
}

// A PR deleted between search and detail fetch is skipped; the other PRs are still saved
// and the run succeeds.
func TestGitHubExtractWorkflow_SkipsNotFoundPullRequest(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub, State: models.IntegrationStateActive, LastSyncedAt: timePtr(now.Add(-30 * time.Minute))}
	gone := ghactivities.PullRequestRef{Owner: "acme", Repo: "backend", Number: 1}
	kept := ghactivities.PullRequestRef{Owner: "acme", Repo: "backend", Number: 2}
	pr := models.PullRequest{Key: uuid.New(), OrgID: integration.OrgID, IntegrationID: integration.Key}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, integration.OrgID, integration.Key).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.Anything).
		Return(ghactivities.SearchPullRequestsOutput{Refs: []ghactivities.PullRequestRef{gone, kept}}, nil).Once()
	env.OnActivity(ghActs.GetPullRequestDetail, mock.Anything, mock.MatchedBy(func(in ghactivities.GetPullRequestDetailInput) bool { return in.Ref.Number == gone.Number })).
		Return(ghactivities.GetPullRequestDetailOutput{}, temporal.NewNonRetryableApplicationError("not found (404)", ghactivities.NotFoundErrorType, nil)).Once()
	env.OnActivity(ghActs.GetPullRequestDetail, mock.Anything, mock.MatchedBy(func(in ghactivities.GetPullRequestDetailInput) bool { return in.Ref.Number == kept.Number })).
		Return(ghactivities.GetPullRequestDetailOutput{Ref: kept, Detail: models.GitHubPullRequestDetail{ID: 2, Number: 2}, SourceRecordKey: uuid.New()}, nil).Once()
	env.OnActivity(ghActs.NormalizePullRequest, mock.Anything, mock.Anything).Return(pr, nil).Once()
	env.OnActivity(ghActs.SavePullRequest, mock.Anything, pr).Return(nil).Once()

	env.SetStartTime(now)
	expectSyncedThrough(env, integration.OrgID, integration.Key, now)

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("expected not-found PR to be skipped, got %v", err)
	}
	env.AssertExpectations(t)
}

// Bad credentials affect every PR, so the run must fail rather than skip them all.
func TestGitHubExtractWorkflow_FailsOnNonSkippableDetailError(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))

	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub, State: models.IntegrationStateActive}
	ref := ghactivities.PullRequestRef{Owner: "acme", Repo: "backend", Number: 1}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, integration.OrgID, integration.Key).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.Anything).
		Return(ghactivities.SearchPullRequestsOutput{Refs: []ghactivities.PullRequestRef{ref}}, nil).Once()
	env.OnActivity(ghActs.GetPullRequestDetail, mock.Anything, mock.Anything).
		Return(ghactivities.GetPullRequestDetailOutput{}, temporal.NewNonRetryableApplicationError("unauthorized (401)", "NonRetryableServiceError", nil)).Once()

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow to fail on a non-skippable error")
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// expectSyncedThrough expects the run to record at as its new sync position.
func expectSyncedThrough(env *testsuite.TestWorkflowEnvironment, orgID, integrationKey uuid.UUID, at time.Time) {
	env.OnActivity(sharedActs.UpdateIntegrationLastSyncedAt, mock.Anything, integrationKey, mock.MatchedBy(func(got time.Time) bool {
		return got.Equal(at)
	}), orgID).Return(nil).Once()
}

// A first sync has no saved position: it searches the backfill window in 24-hour chunks,
// saving each chunk's end and continuing with the next.
func TestGitHubExtractWorkflow_FirstSyncBackfillsInChunks(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(now)

	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub, State: models.IntegrationStateActive}
	backfillStart := now.Add(-30 * 24 * time.Hour)
	firstChunkEnd := backfillStart.Add(24 * time.Hour)

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, integration.OrgID, integration.Key).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(in ghactivities.SearchPullRequestsInput) bool {
		return in.From.Equal(backfillStart) && in.To.Equal(firstChunkEnd)
	})).Return(ghactivities.SearchPullRequestsOutput{}, nil).Once()
	expectSyncedThrough(env, integration.OrgID, integration.Key, firstChunkEnd)

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); !workflow.IsContinueAsNewError(err) {
		t.Fatalf("expected Continue-As-New for the next chunk, got %v", err)
	}
	env.AssertExpectations(t)
}

// After runs failed for hours, the next run searches the whole gap since the last
// successful sync, not just the last 35 minutes.
func TestGitHubExtractWorkflow_ResumesFromLastSuccessfulSync(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(now)

	lastSynced := now.Add(-3 * time.Hour)
	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub, State: models.IntegrationStateActive, LastSyncedAt: &lastSynced}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, integration.OrgID, integration.Key).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(in ghactivities.SearchPullRequestsInput) bool {
		return in.From.Equal(lastSynced.Add(-5*time.Minute)) && in.To.Equal(now)
	})).Return(ghactivities.SearchPullRequestsOutput{}, nil).Once()
	expectSyncedThrough(env, integration.OrgID, integration.Key, now)

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("GitHubExtractWorkflow failed: %v", err)
	}
	env.AssertExpectations(t)
}

// A failed run must not move the sync position, so the next run covers the same range.
func TestGitHubExtractWorkflow_FailedRunKeepsSyncPosition(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(now)

	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub, State: models.IntegrationStateActive, LastSyncedAt: timePtr(now.Add(-time.Hour))}
	ref := ghactivities.PullRequestRef{Owner: "acme", Repo: "backend", Number: 1}

	env.OnActivity(sharedActs.GetIntegration, mock.Anything, integration.OrgID, integration.Key).Return(integration, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.Anything).
		Return(ghactivities.SearchPullRequestsOutput{Refs: []ghactivities.PullRequestRef{ref}}, nil).Once()
	env.OnActivity(ghActs.GetPullRequestDetail, mock.Anything, mock.Anything).
		Return(ghactivities.GetPullRequestDetailOutput{}, temporal.NewNonRetryableApplicationError("unauthorized (401)", "NonRetryableServiceError", nil)).Once()
	saved := false
	env.OnActivity(sharedActs.UpdateIntegrationLastSyncedAt, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, uuid.UUID, time.Time, uuid.UUID) error { saved = true; return nil }).Maybe()

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration, RunOnce: true})

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow error")
	}
	if saved {
		t.Error("a failed run must not save a new sync position")
	}
}
