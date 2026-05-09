package analytics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
	"github.com/datagill/datagill-core/services/search/analytics"
)

func TestPullRequestsPlugin(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	prKey := uuid.New()
	q := analytics.Query{OrgID: uuid.New(), From: now.Add(-24 * time.Hour), To: now, Page: 2, Limit: 10}

	row := repository.PullRequestAnalyticsRow{
		Key:              prKey,
		State:            models.PullRequestStateMerged,
		EnrichmentStatus: models.PREnrichmentStatusCompleted,
		EnrichedAt:       &now,
		Data: models.PullRequestData{
			Title: "feat: add checkout flag",
			Source: models.PullRequestSource{
				RepositoryID: "123456",
				CreatedAt:    ptrTime(now.Add(-24 * time.Hour)),
				MergedAt:     &now,
			},
			Actor: models.PullRequestActor{
				Author: models.PullRequestAuthor{Login: "ariel", ID: 12345},
			},
			Details: models.PullRequestDetails{
				URL:    "https://github.com/acme/web/pull/42",
				Number: 42,
				Repository: models.PullRequestRepository{
					Owner: "acme",
					Name:  "web",
				},
				Labels: []string{"feature"},
				Assignees: []models.PullRequestAssignee{
					{Login: "jordan", ID: 67890},
				},
				Diff: models.PullRequestDiff{
					SHA: "abc123",
					Branches: models.PullRequestDiffBranches{
						Source: "feat/checkout",
						Target: "main",
					},
					Stats: models.PullRequestDiffStats{
						Additions:    120,
						Deletions:    45,
						ChangedFiles: 8,
					},
				},
			},
		},
		EnrichmentData: &models.PullRequestEnrichmentData{
			FeatureFlags: models.PullRequestFeatureFlagRefs{
				Added:   []string{"flag-uuid"},
				Removed: []string{},
			},
		},
	}

	t.Run("returns paginated response with mapped fields", func(t *testing.T) {
		p := analytics.NewPullRequestsPlugin(&fakeAnalyticsRepo{
			pullRequests: repository.PullRequestsResult{Total: 12, Rows: []repository.PullRequestAnalyticsRow{row}},
		})

		if p.Metric() != "pull_requests" {
			t.Fatalf("unexpected metric name: %s", p.Metric())
		}
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		resp := out.(analytics.PullRequestsResponse)
		if resp.Total != 12 || resp.Page != 2 || resp.Count != 1 {
			t.Fatalf("unexpected envelope: %+v", resp)
		}
		got := resp.Data[0]
		if got.Key != prKey || got.Title != row.Data.Title || got.State != models.PullRequestStateMerged {
			t.Errorf("unexpected PR identity fields: %+v", got)
		}
		if got.Repository.Owner != "acme" || got.Repository.Name != "web" || got.Repository.ID != "123456" {
			t.Errorf("unexpected repository: %+v", got.Repository)
		}
		if got.Author.Login != "ariel" || got.Author.ID != 12345 {
			t.Errorf("unexpected author: %+v", got.Author)
		}
		if got.Diff.Stats.ChangedFiles != 8 || got.Diff.Branches.Target != "main" {
			t.Errorf("unexpected diff: %+v", got.Diff)
		}
		if got.Enrichment.Status != models.PREnrichmentStatusCompleted || len(got.Enrichment.FeatureFlags.Added) != 1 {
			t.Errorf("unexpected enrichment: %+v", got.Enrichment)
		}
	})

	t.Run("nil enrichment data returns empty feature flag arrays", func(t *testing.T) {
		row.EnrichmentData = nil
		row.EnrichmentStatus = models.PREnrichmentStatusPending
		p := analytics.NewPullRequestsPlugin(&fakeAnalyticsRepo{
			pullRequests: repository.PullRequestsResult{Total: 1, Rows: []repository.PullRequestAnalyticsRow{row}},
		})

		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		enrichment := out.(analytics.PullRequestsResponse).Data[0].Enrichment
		if enrichment.FeatureFlags.Added == nil || enrichment.FeatureFlags.Removed == nil {
			t.Fatalf("expected non-nil empty feature flag slices, got %+v", enrichment.FeatureFlags)
		}
	})

	t.Run("repo error propagates", func(t *testing.T) {
		p := analytics.NewPullRequestsPlugin(&fakeAnalyticsRepo{pullRequestsErr: errors.New("db error")})
		if _, err := p.Execute(context.Background(), q); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("ResponseType returns PullRequestsResponse", func(t *testing.T) {
		p := analytics.NewPullRequestsPlugin(&fakeAnalyticsRepo{})
		if _, ok := p.ResponseType().(analytics.PullRequestsResponse); !ok {
			t.Errorf("expected PullRequestsResponse, got %T", p.ResponseType())
		}
	})
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
