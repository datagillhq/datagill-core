package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/shared"
)

type githubRepository interface {
	GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
	SaveSourceRecord(ctx context.Context, r models.SourceRecord) error
	SavePullRequest(ctx context.Context, pr models.PullRequest) error
}

// Activities contains Temporal activities for GitHub ETL.
type Activities struct {
	client *Client
	repo   githubRepository
}

// New creates a new Activities instance.
func New(client *Client, repo githubRepository) *Activities {
	return &Activities{client: client, repo: repo}
}

func (a *Activities) getInstallationToken(ctx context.Context, orgID, key uuid.UUID) (InstallationToken, error) {
	if a.client == nil {
		return InstallationToken{}, fmt.Errorf("github client is not configured")
	}

	integration, err := a.repo.GetIntegration(ctx, orgID, key)
	if err != nil {
		return InstallationToken{}, fmt.Errorf("get integration for client: %w", err)
	}
	if integration.Source != models.SourceGitHub {
		return InstallationToken{}, fmt.Errorf("integration source %q is not github", integration.Source)
	}
	if integration.Config.InstallationID == 0 {
		return InstallationToken{}, fmt.Errorf("github integration missing installation_id")
	}

	token, err := a.client.CreateInstallationToken(ctx, integration.Config.InstallationID)
	if err != nil {
		return InstallationToken{}, toServiceError(fmt.Errorf("create installation token: %w", err))
	}
	return token, nil
}

// SearchPullRequestsInput is the input for the SearchPullRequests activity.
type SearchPullRequestsInput struct {
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
	From           time.Time
	To             time.Time
	Page           int
	PerPage        int
}

// PullRequestRef is the discovery output used by later PR detail fetches.
type PullRequestRef struct {
	SourceKey       string
	Owner           string
	Repo            string
	Number          int64
	Labels          []string
	Assignees       []models.PullRequestAssignee
	SourceRecordKey uuid.UUID
}

// SearchPullRequestsOutput is the output from the SearchPullRequests activity.
type SearchPullRequestsOutput struct {
	Refs              []PullRequestRef
	TotalCount        int
	IncompleteResults bool
	NextPage          int
}

// GetPullRequestDetailInput is the input for the GetPullRequestDetail activity.
type GetPullRequestDetailInput struct {
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
	Ref            PullRequestRef
}

// GetPullRequestDetailOutput is the output from the GetPullRequestDetail activity.
type GetPullRequestDetailOutput struct {
	Ref             PullRequestRef
	Detail          models.GitHubPullRequestDetail
	SourceRecordKey uuid.UUID
}

// NormalizePullRequestInput is the input for the NormalizePullRequest activity.
type NormalizePullRequestInput struct {
	IntegrationKey  uuid.UUID
	OrgID           uuid.UUID
	SourceRecordKey uuid.UUID
	Ref             PullRequestRef
	Detail          models.GitHubPullRequestDetail
}

// SearchPullRequests queries GitHub's search API for merged pull requests in a date window
// and persists each raw search hit as a pr_search source record.
func (a *Activities) SearchPullRequests(ctx context.Context, input SearchPullRequestsInput) (SearchPullRequestsOutput, error) {
	token, err := a.getInstallationToken(ctx, input.OrgID, input.IntegrationKey)
	if err != nil {
		return SearchPullRequestsOutput{}, err
	}

	integration, err := a.repo.GetIntegration(ctx, input.OrgID, input.IntegrationKey)
	if err != nil {
		return SearchPullRequestsOutput{}, fmt.Errorf("get integration for search: %w", err)
	}
	accountLogin := integration.Config.AccountLogin
	if accountLogin == "" {
		return SearchPullRequestsOutput{}, &shared.NonRetryableServiceError{Message: "github integration missing account_login"}
	}
	accountType := integration.Config.AccountType
	if accountType == "" {
		return SearchPullRequestsOutput{}, &shared.NonRetryableServiceError{Message: "github integration missing account_type"}
	}

	resp, err := a.client.SearchPullRequests(ctx, token.Token, accountLogin, accountType, input.From, input.To, input.Page, input.PerPage)
	if err != nil {
		return SearchPullRequestsOutput{}, toServiceError(fmt.Errorf("search pull requests: %w", err))
	}

	refs := make([]PullRequestRef, 0, len(resp.Items))
	for _, item := range resp.Items {
		if item.PullRequest == nil {
			continue
		}

		owner, repo, err := parseRepositoryURL(item.RepositoryURL)
		if err != nil {
			return SearchPullRequestsOutput{}, &shared.NonRetryableServiceError{
				Message: fmt.Sprintf("parse repository url for search result %d", item.ID),
				Cause:   err,
			}
		}

		sourceKey := strconv.FormatInt(item.ID, 10)
		sourceRecordKey := models.ComputeSourceRecordKey(input.OrgID, models.SourceGitHub, models.SourceTypePRSearch, sourceKey)
		raw, err := json.Marshal(item)
		if err != nil {
			return SearchPullRequestsOutput{}, fmt.Errorf("marshal search result %d: %w", item.ID, err)
		}
		if err := a.repo.SaveSourceRecord(ctx, models.SourceRecord{
			Key:           sourceRecordKey,
			IntegrationID: input.IntegrationKey,
			OrgID:         input.OrgID,
			SourceType:    models.SourceTypePRSearch,
			Data:          raw,
			CreatedAt:     time.Now().UTC(),
		}); err != nil {
			return SearchPullRequestsOutput{}, fmt.Errorf("save source record %d: %w", item.ID, err)
		}

		refs = append(refs, PullRequestRef{
			SourceKey:       sourceKey,
			Owner:           owner,
			Repo:            repo,
			Number:          item.Number,
			Labels:          labelNames(item.Labels),
			Assignees:       pullRequestAssignees(item.Assignees),
			SourceRecordKey: sourceRecordKey,
		})
	}

	nextPage := 0
	page := input.Page
	if page <= 0 {
		page = 1
	}
	perPage := input.PerPage
	if perPage <= 0 || perPage > searchMaxPerPage {
		perPage = searchMaxPerPage
	}
	if len(resp.Items) == perPage && page*perPage < resp.TotalCount && page*perPage < searchResultCap {
		nextPage = page + 1
	}

	return SearchPullRequestsOutput{
		Refs:              refs,
		TotalCount:        resp.TotalCount,
		IncompleteResults: resp.IncompleteResults,
		NextPage:          nextPage,
	}, nil
}

// GetPullRequestDetail fetches a full pull request payload and persists it as
// a pr_detail source record before normalization.
func (a *Activities) GetPullRequestDetail(ctx context.Context, input GetPullRequestDetailInput) (GetPullRequestDetailOutput, error) {
	if input.Ref.Owner == "" {
		return GetPullRequestDetailOutput{}, &shared.NonRetryableServiceError{Message: "github pull request ref missing owner"}
	}
	if input.Ref.Repo == "" {
		return GetPullRequestDetailOutput{}, &shared.NonRetryableServiceError{Message: "github pull request ref missing repo"}
	}
	if input.Ref.Number <= 0 {
		return GetPullRequestDetailOutput{}, &shared.NonRetryableServiceError{Message: "github pull request ref missing number"}
	}

	token, err := a.getInstallationToken(ctx, input.OrgID, input.IntegrationKey)
	if err != nil {
		return GetPullRequestDetailOutput{}, err
	}

	resp, err := a.client.GetPullRequestDetail(ctx, token.Token, input.Ref.Owner, input.Ref.Repo, input.Ref.Number)
	if err != nil {
		return GetPullRequestDetailOutput{}, toServiceError(fmt.Errorf("get pull request detail: %w", err))
	}
	detail := resp.Detail
	if detail.ID == 0 {
		return GetPullRequestDetailOutput{}, &shared.NonRetryableServiceError{Message: "github pull request detail missing id"}
	}

	sourceKey := strconv.FormatInt(detail.ID, 10)
	sourceRecordKey := models.ComputeSourceRecordKey(input.OrgID, models.SourceGitHub, models.SourceTypePRDetail, sourceKey)
	if err := a.repo.SaveSourceRecord(ctx, models.SourceRecord{
		Key:           sourceRecordKey,
		IntegrationID: input.IntegrationKey,
		OrgID:         input.OrgID,
		SourceType:    models.SourceTypePRDetail,
		Data:          resp.Raw,
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		return GetPullRequestDetailOutput{}, fmt.Errorf("save pull request detail source record %d: %w", detail.ID, err)
	}

	return GetPullRequestDetailOutput{Ref: input.Ref, Detail: detail, SourceRecordKey: sourceRecordKey}, nil
}

// NormalizePullRequest maps a raw GitHub PR detail payload into DataGill's
// canonical pull_requests shape.
func (a *Activities) NormalizePullRequest(_ context.Context, input NormalizePullRequestInput) (models.PullRequest, error) {
	if input.Detail.ID == 0 {
		return models.PullRequest{}, &shared.NonRetryableServiceError{Message: "github pull request detail missing id"}
	}
	if input.SourceRecordKey == uuid.Nil {
		return models.PullRequest{}, &shared.NonRetryableServiceError{Message: "github pull request missing source_record_key"}
	}

	sourceKey := strconv.FormatInt(input.Detail.ID, 10)
	owner := input.Detail.Base.Repo.Owner.Login
	if owner == "" {
		owner = input.Ref.Owner
	}
	repo := input.Detail.Base.Repo.Name
	if repo == "" {
		repo = input.Ref.Repo
	}

	createdAt := optionalTime(input.Detail.CreatedAt)
	now := time.Now().UTC()

	return models.PullRequest{
		Key:             models.ComputePullRequestKey(input.OrgID, sourceKey),
		SourceRecordKey: input.SourceRecordKey,
		IntegrationID:   input.IntegrationKey,
		OrgID:           input.OrgID,
		State:           models.PullRequestStateMerged,
		Data: models.PullRequestData{
			Title:       input.Detail.Title,
			Description: input.Detail.Body,
			Source: models.PullRequestSource{
				Key:          sourceKey,
				Provider:     models.SourceGitHub,
				RepositoryID: repositoryID(input.Detail),
				CreatedAt:    createdAt,
				MergedAt:     input.Detail.MergedAt,
			},
			Actor: models.PullRequestActor{
				Author: models.PullRequestAuthor{
					Login: input.Detail.User.Login,
					ID:    input.Detail.User.ID,
				},
			},
			Details: models.PullRequestDetails{
				URL:    input.Detail.HTMLURL,
				Number: input.Detail.Number,
				Repository: models.PullRequestRepository{
					Owner: owner,
					Name:  repo,
				},
				Labels:    input.Ref.Labels,
				Assignees: input.Ref.Assignees,
				Diff: models.PullRequestDiff{
					URL: input.Detail.DiffURL,
					SHA: input.Detail.MergeCommitSHA,
					Branches: models.PullRequestDiffBranches{
						Source: input.Detail.Head.Ref,
						Target: input.Detail.Base.Ref,
					},
					Stats: models.PullRequestDiffStats{
						Additions:    input.Detail.Additions,
						Deletions:    input.Detail.Deletions,
						ChangedFiles: input.Detail.ChangedFiles,
					},
				},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// SavePullRequest persists a normalized pull request.
func (a *Activities) SavePullRequest(ctx context.Context, pr models.PullRequest) error {
	if err := a.repo.SavePullRequest(ctx, pr); err != nil {
		return fmt.Errorf("save pull request: %w", err)
	}
	return nil
}

func parseRepositoryURL(raw string) (owner, repo string, err error) {
	const prefix = "https://api.github.com/repos/"
	if !strings.HasPrefix(raw, prefix) {
		return "", "", fmt.Errorf("unexpected repository_url %q", raw)
	}
	parts := strings.Split(strings.TrimPrefix(raw, prefix), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("unexpected repository_url %q", raw)
	}
	return parts[0], parts[1], nil
}

func repositoryID(detail models.GitHubPullRequestDetail) string {
	if detail.Base.Repo.ID == 0 {
		return ""
	}
	return strconv.FormatInt(detail.Base.Repo.ID, 10)
}

func optionalTime(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}

func labelNames(labels []models.GitHubLabelSummary) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if label.Name == "" {
			continue
		}
		out = append(out, label.Name)
	}
	return out
}

func pullRequestAssignees(users []models.GitHubUserSummary) []models.PullRequestAssignee {
	if len(users) == 0 {
		return nil
	}
	out := make([]models.PullRequestAssignee, 0, len(users))
	for _, user := range users {
		if user.Login == "" && user.ID == 0 {
			continue
		}
		out = append(out, models.PullRequestAssignee{
			Login: user.Login,
			ID:    user.ID,
		})
	}
	return out
}
