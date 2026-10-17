//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/datagill/datagill-core/models"
)

func TestSavePullRequest(t *testing.T) {
	ctx := context.Background()
	repo, err := NewPostgresRepository(ctx, testConnString, testEncryptionKey)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgKey := setupTestOrg(ctx, t, pool)
	integrationKey := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, 'github', $3, '{}'::jsonb, 'active', NOW(), NOW())
	`, integrationKey, orgKey, mustEncryptTestAPIKey(t, "")); err != nil {
		t.Fatalf("failed to create github integration: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	pr := models.PullRequest{
		Key:             models.ComputePullRequestKey(orgKey, "987654321"),
		SourceRecordKey: uuid.New(),
		IntegrationID:   integrationKey,
		OrgID:           orgKey,
		State:           models.PullRequestStateMerged,
		Data: models.PullRequestData{
			Title:       "feat: add checkout v2",
			Description: "Adds the new checkout flow",
			Source: models.PullRequestSource{
				Key:          "987654321",
				Provider:     models.SourceGitHub,
				RepositoryID: "123456",
				CreatedAt:    ptrTime(now.Add(-24 * time.Hour)),
				MergedAt:     ptrTime(now),
			},
			Actor: models.PullRequestActor{
				Author: models.PullRequestAuthor{
					Login: "ariel",
					ID:    12345,
					Email: "ariel@example.com",
				},
			},
			Details: models.PullRequestDetails{
				URL:    "https://github.com/acme/backend/pull/42",
				Number: 42,
				Repository: models.PullRequestRepository{
					Owner: "acme",
					Name:  "backend",
				},
				Labels: []string{"feature", "payments"},
				Assignees: []models.PullRequestAssignee{
					{Login: "jordan", ID: 67890},
				},
				Diff: models.PullRequestDiff{
					URL: "https://github.com/acme/backend/pull/42.diff",
					SHA: "abc123",
					Branches: models.PullRequestDiffBranches{
						Source: "feat/checkout-v2",
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
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := repo.SavePullRequest(ctx, pr); err != nil {
		t.Fatalf("SavePullRequest failed: %v", err)
	}

	var (
		state     string
		title     string
		createdAt time.Time
		updatedAt time.Time
	)
	err = pool.QueryRow(ctx, `
		SELECT state, data->>'title', created_at, updated_at
		FROM pull_requests
		WHERE key = $1
	`, pr.Key).Scan(&state, &title, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("failed to query pull request: %v", err)
	}
	if state != string(models.PullRequestStateMerged) {
		t.Errorf("expected state 'merged', got %q", state)
	}
	if title != pr.Data.Title {
		t.Errorf("expected title %q, got %q", pr.Data.Title, title)
	}
	if !createdAt.Equal(pr.CreatedAt) {
		t.Errorf("expected created_at %s, got %s", pr.CreatedAt, createdAt)
	}

	pr.SourceRecordKey = uuid.New()
	pr.State = models.PullRequestStateClosed
	pr.Data.Title = "feat: add checkout v2 (updated)"
	pr.CreatedAt = now.Add(-48 * time.Hour)
	pr.UpdatedAt = now.Add(time.Hour)

	if err := repo.SavePullRequest(ctx, pr); err != nil {
		t.Fatalf("SavePullRequest upsert failed: %v", err)
	}

	var count int
	err = pool.QueryRow(ctx, `
		SELECT state, data->>'title', created_at, updated_at
		FROM pull_requests
		WHERE key = $1
	`, pr.Key).Scan(&state, &title, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("failed to query updated pull request: %v", err)
	}
	if state != string(models.PullRequestStateClosed) {
		t.Errorf("expected state 'closed' after update, got %q", state)
	}
	if title != pr.Data.Title {
		t.Errorf("expected updated title %q, got %q", pr.Data.Title, title)
	}
	if !createdAt.Equal(now) {
		t.Errorf("expected created_at to remain %s, got %s", now, createdAt)
	}
	if !updatedAt.Equal(pr.UpdatedAt) {
		t.Errorf("expected updated_at to change to %s, got %s", pr.UpdatedAt, updatedAt)
	}

	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM pull_requests WHERE key = $1", pr.Key).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count pull requests: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 pull request, got %d", count)
	}
}

func TestQueryPullRequests(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	otherOrgKey := setupTestOrg(ctx, t, pool)
	integrationKey := setupTestGitHubIntegration(ctx, t, pool, orgKey)
	otherIntegrationKey := setupTestGitHubIntegration(ctx, t, pool, otherOrgKey)

	now := time.Now().UTC().Truncate(time.Microsecond)
	windowStart := now.Add(-2 * time.Hour)
	windowEnd := now.Add(2 * time.Hour)

	oldPR := saveAnalyticsPR(ctx, t, repo, orgKey, integrationKey, "old", now.Add(-90*time.Minute), models.PullRequestStateMerged)
	newPR := saveAnalyticsPR(ctx, t, repo, orgKey, integrationKey, "new", now.Add(-15*time.Minute), models.PullRequestStateMerged)
	saveAnalyticsPR(ctx, t, repo, orgKey, integrationKey, "outside", now.Add(-24*time.Hour), models.PullRequestStateMerged)
	saveAnalyticsPR(ctx, t, repo, orgKey, integrationKey, "closed", now.Add(-10*time.Minute), models.PullRequestStateClosed)
	saveAnalyticsPR(ctx, t, repo, otherOrgKey, otherIntegrationKey, "other-org", now.Add(-5*time.Minute), models.PullRequestStateMerged)
	saveAnalyticsPR(ctx, t, repo, orgKey, integrationKey, "nil-merged", time.Time{}, models.PullRequestStateMerged)

	mustClaim(ctx, t, repo, newPR.Key)
	if err := repo.SaveEnrichmentResult(ctx, newPR.Key, models.PullRequestEnrichmentData{
		FeatureFlags: models.PullRequestFeatureFlagRefs{
			Added:   []string{"flag-added"},
			Removed: []string{"flag-removed"},
		},
	}); err != nil {
		t.Fatalf("SaveEnrichmentResult: %v", err)
	}

	res, err := repo.QueryPullRequests(ctx, orgKey, windowStart, windowEnd, 1, 25)
	if err != nil {
		t.Fatalf("QueryPullRequests: %v", err)
	}
	if res.Total != 2 || len(res.Rows) != 2 {
		t.Fatalf("expected total=2 rows=2, got total=%d rows=%d", res.Total, len(res.Rows))
	}
	if res.Rows[0].Key != newPR.Key || res.Rows[1].Key != oldPR.Key {
		t.Fatalf("expected newest merged PRs first, got %v then %v", res.Rows[0].Key, res.Rows[1].Key)
	}
	if res.Rows[0].EnrichmentStatus != models.PREnrichmentStatusCompleted {
		t.Errorf("expected completed enrichment, got %q", res.Rows[0].EnrichmentStatus)
	}
	if res.Rows[0].EnrichmentData == nil || len(res.Rows[0].EnrichmentData.FeatureFlags.Added) != 1 {
		t.Errorf("expected enrichment feature flags, got %+v", res.Rows[0].EnrichmentData)
	}
	if res.Rows[1].EnrichmentStatus != models.PREnrichmentStatusPending {
		t.Errorf("expected pending enrichment for nil enrichment JSON, got %q", res.Rows[1].EnrichmentStatus)
	}
	if res.Rows[1].EnrichmentData != nil {
		t.Errorf("expected nil enrichment data, got %+v", res.Rows[1].EnrichmentData)
	}

	res, err = repo.QueryPullRequests(ctx, orgKey, windowStart, windowEnd, 2, 1)
	if err != nil {
		t.Fatalf("QueryPullRequests page 2: %v", err)
	}
	if res.Total != 2 || len(res.Rows) != 1 || res.Rows[0].Key != oldPR.Key {
		t.Fatalf("expected page 2 to contain old PR with total=2, got %+v", res)
	}

	res, err = repo.QueryPullRequests(ctx, orgKey, windowStart, windowEnd, 3, 1)
	if err != nil {
		t.Fatalf("QueryPullRequests page past last: %v", err)
	}
	if res.Total != 2 || len(res.Rows) != 0 {
		t.Fatalf("expected page past last to preserve total=2 with no rows, got %+v", res)
	}
}

func mustEncryptTestAPIKey(t *testing.T, apiKey string) string {
	t.Helper()
	encrypted, err := encryptAPIKey(testEncryptionKey, apiKey)
	if err != nil {
		t.Fatalf("failed to encrypt test api key: %v", err)
	}
	return encrypted
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func setupTestGitHubIntegration(ctx context.Context, t *testing.T, pool *pgxpool.Pool, orgKey uuid.UUID) uuid.UUID {
	t.Helper()
	integrationKey := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, 'github', $3, '{}'::jsonb, 'active', NOW(), NOW())
	`, integrationKey, orgKey, mustEncryptTestAPIKey(t, "")); err != nil {
		t.Fatalf("failed to create github integration: %v", err)
	}
	return integrationKey
}

func saveAnalyticsPR(ctx context.Context, t *testing.T, repo *PostgresRepository, orgKey, integrationKey uuid.UUID, sourceKey string, mergedAt time.Time, state models.PullRequestState) models.PullRequest {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Microsecond)
	var mergedAtPtr *time.Time
	if !mergedAt.IsZero() {
		merged := mergedAt.UTC().Truncate(time.Microsecond)
		mergedAtPtr = &merged
	}
	pr := models.PullRequest{
		Key:             models.ComputePullRequestKey(orgKey, sourceKey),
		SourceRecordKey: uuid.New(),
		IntegrationID:   integrationKey,
		OrgID:           orgKey,
		State:           state,
		Data: models.PullRequestData{
			Title: "PR " + sourceKey,
			Source: models.PullRequestSource{
				Key:          sourceKey,
				Provider:     models.SourceGitHub,
				RepositoryID: "repo-" + sourceKey,
				CreatedAt:    ptrTime(now.Add(-24 * time.Hour)),
				MergedAt:     mergedAtPtr,
			},
			Actor: models.PullRequestActor{
				Author: models.PullRequestAuthor{Login: "author-" + sourceKey, ID: 123},
			},
			Details: models.PullRequestDetails{
				URL:    "https://github.com/acme/web/pull/1",
				Number: 1,
				Repository: models.PullRequestRepository{
					Owner: "acme",
					Name:  "web",
				},
				Diff: models.PullRequestDiff{
					SHA: "abc123",
					Branches: models.PullRequestDiffBranches{
						Source: "feature",
						Target: "main",
					},
					Stats: models.PullRequestDiffStats{Additions: 1, Deletions: 2, ChangedFiles: 3},
				},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repo.SavePullRequest(ctx, pr); err != nil {
		t.Fatalf("SavePullRequest %s: %v", sourceKey, err)
	}
	return pr
}
