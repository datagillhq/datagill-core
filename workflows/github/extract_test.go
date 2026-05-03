package github_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	ghactivities "github.com/datagill/datagill-core/activities/github"
	"github.com/datagill/datagill-core/models"
	ghworkflows "github.com/datagill/datagill-core/workflows/github"
)

var ghActs = &ghactivities.Activities{}

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

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration})

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
	}

	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.From.Equal(now.Add(-35*time.Minute)) && input.To.Equal(now)
	})).Return(ghactivities.SearchPullRequestsOutput{TotalCount: 901}, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.From.Equal(now.Add(-35*time.Minute)) && input.To.Equal(now.Add(-17*time.Minute-30*time.Second))
	})).Return(ghactivities.SearchPullRequestsOutput{}, nil).Once()
	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.From.Equal(now.Add(-17*time.Minute-30*time.Second)) && input.To.Equal(now)
	})).Return(ghactivities.SearchPullRequestsOutput{}, nil).Once()

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration})

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
	}

	env.OnActivity(ghActs.SearchPullRequests, mock.Anything, mock.MatchedBy(func(input ghactivities.SearchPullRequestsInput) bool {
		return input.From.Equal(now.Add(-35*time.Minute)) && input.To.Equal(now)
	})).Return(ghactivities.SearchPullRequestsOutput{}, errors.New("github search failed")).Once()

	env.ExecuteWorkflow(ghworkflows.GitHubExtractWorkflow, ghworkflows.ExtractInput{Integration: integration})

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow error")
	}
	env.AssertExpectations(t)
}
