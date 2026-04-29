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
	)
	err = pool.QueryRow(ctx, `
		SELECT state, data->>'title', created_at
		FROM pull_requests
		WHERE key = $1
	`, pr.Key).Scan(&state, &title, &createdAt)
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
		SELECT state, data->>'title', created_at
		FROM pull_requests
		WHERE key = $1
	`, pr.Key).Scan(&state, &title, &createdAt)
	if err != nil {
		t.Fatalf("failed to query updated pull request: %v", err)
	}
	if state != string(models.PullRequestStateClosed) {
		t.Errorf("expected state 'closed' after update, got %q", state)
	}
	if title != pr.Data.Title {
		t.Errorf("expected updated title %q, got %q", pr.Data.Title, title)
	}
	if !createdAt.Equal(pr.CreatedAt) {
		t.Errorf("expected created_at to refresh to %s, got %s", pr.CreatedAt, createdAt)
	}

	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM pull_requests WHERE key = $1", pr.Key).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count pull requests: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 pull request, got %d", count)
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
