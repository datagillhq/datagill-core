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
	SourceRecordKey uuid.UUID
}

// SearchPullRequestsOutput is the output from the SearchPullRequests activity.
type SearchPullRequestsOutput struct {
	Refs              []PullRequestRef
	TotalCount        int
	IncompleteResults bool
	NextPage          int
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
