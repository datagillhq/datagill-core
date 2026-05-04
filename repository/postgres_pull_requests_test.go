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

func TestEnrichmentLifecycle(t *testing.T) {
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
		t.Fatalf("setup integration: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	pr := models.PullRequest{
		Key:             models.ComputePullRequestKey(orgKey, "enrich-test-1"),
		SourceRecordKey: uuid.New(),
		IntegrationID:   integrationKey,
		OrgID:           orgKey,
		State:           models.PullRequestStateMerged,
		Data:            models.PullRequestData{Title: "enrichment test PR"},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := repo.SavePullRequest(ctx, pr); err != nil {
		t.Fatalf("SavePullRequest: %v", err)
	}

	// Newly inserted PR should be pending with 0 attempts.
	pending, err := repo.ListPendingPRsForEnrichment(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingPRsForEnrichment: %v", err)
	}
	var found bool
	for _, p := range pending {
		if p.Key == pr.Key {
			found = true
			if p.Status != models.PREnrichmentStatusPending {
				t.Errorf("expected status pending, got %q", p.Status)
			}
			if p.Attempts != 0 {
				t.Errorf("expected 0 attempts, got %d", p.Attempts)
			}
		}
	}
	if !found {
		t.Fatal("newly saved PR not found in pending list")
	}

	// Claim sets status to in_progress and increments attempts.
	if err := repo.ClaimPRForEnrichment(ctx, pr.Key); err != nil {
		t.Fatalf("ClaimPRForEnrichment: %v", err)
	}
	var (
		status   string
		attempts int
	)
	pool.QueryRow(ctx, `SELECT status, attempts FROM pull_requests WHERE key = $1`, pr.Key).Scan(&status, &attempts)
	if status != "in_progress" {
		t.Errorf("expected in_progress after claim, got %q", status)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt after claim, got %d", attempts)
	}

	// SaveEnrichmentResult sets status to completed and writes enrichment.
	enrichment := models.PullRequestEnrichment{
		FeatureFlags: models.PullRequestFeatureFlagRefs{
			Added:   []string{"flag-uuid-1"},
			Removed: []string{"flag-uuid-2"},
		},
	}
	if err := repo.SaveEnrichmentResult(ctx, pr.Key, enrichment); err != nil {
		t.Fatalf("SaveEnrichmentResult: %v", err)
	}
	pool.QueryRow(ctx, `SELECT status FROM pull_requests WHERE key = $1`, pr.Key).Scan(&status)
	if status != "completed" {
		t.Errorf("expected completed after save, got %q", status)
	}

	// Completed PR must not appear in pending list.
	pending, _ = repo.ListPendingPRsForEnrichment(ctx, 10)
	for _, p := range pending {
		if p.Key == pr.Key {
			t.Error("completed PR should not appear in pending list")
		}
	}
}

func TestMarkEnrichmentFailed_RetryBelowCap(t *testing.T) {
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
	pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, 'github', $3, '{}'::jsonb, 'active', NOW(), NOW())
	`, integrationKey, orgKey, mustEncryptTestAPIKey(t, ""))

	now := time.Now().UTC().Truncate(time.Microsecond)
	pr := models.PullRequest{
		Key:           models.ComputePullRequestKey(orgKey, "enrich-retry-test"),
		SourceRecordKey: uuid.New(),
		IntegrationID: integrationKey,
		OrgID:         orgKey,
		State:         models.PullRequestStateMerged,
		Data:          models.PullRequestData{Title: "retry test"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	repo.SavePullRequest(ctx, pr)

	// Claim once (attempts = 1), then fail — should reset to pending.
	repo.ClaimPRForEnrichment(ctx, pr.Key)
	if err := repo.MarkEnrichmentFailed(ctx, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed: %v", err)
	}
	var status string
	pool.QueryRow(ctx, `SELECT status FROM pull_requests WHERE key = $1`, pr.Key).Scan(&status)
	if status != "pending" {
		t.Errorf("expected pending after first failure, got %q", status)
	}
}

func TestMarkEnrichmentFailed_CapReached(t *testing.T) {
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
	pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, 'github', $3, '{}'::jsonb, 'active', NOW(), NOW())
	`, integrationKey, orgKey, mustEncryptTestAPIKey(t, ""))

	now := time.Now().UTC().Truncate(time.Microsecond)
	pr := models.PullRequest{
		Key:           models.ComputePullRequestKey(orgKey, "enrich-cap-test"),
		SourceRecordKey: uuid.New(),
		IntegrationID: integrationKey,
		OrgID:         orgKey,
		State:         models.PullRequestStateMerged,
		Data:          models.PullRequestData{Title: "cap test"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	repo.SavePullRequest(ctx, pr)

	// Simulate 5 claim+fail cycles to hit the retry cap.
	for i := 0; i < enrichmentMaxAttempts; i++ {
		pool.Exec(ctx, `UPDATE pull_requests SET status = 'pending' WHERE key = $1`, pr.Key)
		repo.ClaimPRForEnrichment(ctx, pr.Key)
	}
	if err := repo.MarkEnrichmentFailed(ctx, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed at cap: %v", err)
	}
	var status string
	pool.QueryRow(ctx, `SELECT status FROM pull_requests WHERE key = $1`, pr.Key).Scan(&status)
	if status != "failed" {
		t.Errorf("expected failed after cap reached, got %q", status)
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
