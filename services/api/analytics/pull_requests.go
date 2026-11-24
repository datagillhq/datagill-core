package analytics

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// PullRequestEntry is a single GitHub pull request in the pull_requests response.
type PullRequestEntry struct {
	Key        uuid.UUID                  `json:"key"`
	Title      string                     `json:"title"`
	State      models.PullRequestState    `json:"state"`
	URL        string                     `json:"url"`
	Number     int64                      `json:"number"`
	Repository PullRequestRepositoryEntry `json:"repository"`
	Author     PullRequestAuthorEntry     `json:"author"`
	Labels     []string                   `json:"labels"`
	Assignees  []PullRequestAssigneeEntry `json:"assignees"`
	CreatedAt  *time.Time                 `json:"createdAt,omitempty"`
	MergedAt   *time.Time                 `json:"mergedAt,omitempty"`
	Diff       PullRequestDiffEntry       `json:"diff"`
	Enrichment PullRequestEnrichmentEntry `json:"enrichment"`
}

type PullRequestRepositoryEntry struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
	ID    string `json:"id"`
}

type PullRequestAuthorEntry struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

type PullRequestAssigneeEntry struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

type PullRequestDiffEntry struct {
	SHA      string                       `json:"sha"`
	Branches PullRequestDiffBranchesEntry `json:"branches"`
	Stats    PullRequestDiffStatsEntry    `json:"stats"`
}

type PullRequestDiffBranchesEntry struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type PullRequestDiffStatsEntry struct {
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	ChangedFiles int `json:"changedFiles"`
}

type PullRequestEnrichmentEntry struct {
	Status       models.PREnrichmentStatus       `json:"status"`
	EnrichedAt   *time.Time                      `json:"enrichedAt,omitempty"`
	FeatureFlags PullRequestFeatureFlagRefsEntry `json:"featureFlags"`
}

type PullRequestFeatureFlagRefsEntry struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// PullRequestsResponse is the pull_requests plugin response envelope.
type PullRequestsResponse struct {
	Total int                `json:"total"`
	Page  int                `json:"page"`
	Count int                `json:"count"`
	Data  []PullRequestEntry `json:"data"`
}

type pullRequestsPlugin struct{ repo AnalyticsRepository }

// NewPullRequestsPlugin returns a plugin for the "pull_requests" metric.
func NewPullRequestsPlugin(repo AnalyticsRepository) Plugin {
	return &pullRequestsPlugin{repo: repo}
}

func (p *pullRequestsPlugin) Metric() string    { return "pull_requests" }
func (p *pullRequestsPlugin) ResponseType() any { return PullRequestsResponse{} }

func (p *pullRequestsPlugin) Execute(ctx context.Context, q Query) (any, error) {
	res, err := p.repo.QueryPullRequests(ctx, q.OrgID, q.From, q.To, q.Page, q.Limit)
	if err != nil {
		return nil, err
	}

	entries := make([]PullRequestEntry, 0, len(res.Rows))
	for _, row := range res.Rows {
		entries = append(entries, mapPullRequestEntry(row.Key, row.State, row.Data, row.EnrichmentStatus, row.EnrichmentData, row.EnrichedAt))
	}
	return PullRequestsResponse{
		Total: res.Total,
		Page:  q.Page,
		Count: len(entries),
		Data:  entries,
	}, nil
}

func mapPullRequestEntry(key uuid.UUID, state models.PullRequestState, data models.PullRequestData, status models.PREnrichmentStatus, enrichment *models.PullRequestEnrichmentData, enrichedAt *time.Time) PullRequestEntry {
	labels := data.Details.Labels
	if labels == nil {
		labels = []string{}
	}

	assignees := make([]PullRequestAssigneeEntry, 0, len(data.Details.Assignees))
	for _, a := range data.Details.Assignees {
		assignees = append(assignees, PullRequestAssigneeEntry{Login: a.Login, ID: a.ID})
	}

	added := []string{}
	removed := []string{}
	if enrichment != nil {
		if enrichment.FeatureFlags.Added != nil {
			added = enrichment.FeatureFlags.Added
		}
		if enrichment.FeatureFlags.Removed != nil {
			removed = enrichment.FeatureFlags.Removed
		}
	}

	return PullRequestEntry{
		Key:    key,
		Title:  data.Title,
		State:  state,
		URL:    data.Details.URL,
		Number: data.Details.Number,
		Repository: PullRequestRepositoryEntry{
			Owner: data.Details.Repository.Owner,
			Name:  data.Details.Repository.Name,
			ID:    data.Source.RepositoryID,
		},
		Author: PullRequestAuthorEntry{
			Login: data.Actor.Author.Login,
			ID:    data.Actor.Author.ID,
		},
		Labels:    labels,
		Assignees: assignees,
		CreatedAt: data.Source.CreatedAt,
		MergedAt:  data.Source.MergedAt,
		Diff: PullRequestDiffEntry{
			SHA: data.Details.Diff.SHA,
			Branches: PullRequestDiffBranchesEntry{
				Source: data.Details.Diff.Branches.Source,
				Target: data.Details.Diff.Branches.Target,
			},
			Stats: PullRequestDiffStatsEntry{
				Additions:    data.Details.Diff.Stats.Additions,
				Deletions:    data.Details.Diff.Stats.Deletions,
				ChangedFiles: data.Details.Diff.Stats.ChangedFiles,
			},
		},
		Enrichment: PullRequestEnrichmentEntry{
			Status:     status,
			EnrichedAt: enrichedAt,
			FeatureFlags: PullRequestFeatureFlagRefsEntry{
				Added:   added,
				Removed: removed,
			},
		},
	}
}
