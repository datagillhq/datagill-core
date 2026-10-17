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

func TestSavePullRequest_CreatesEnrichmentRow(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-auto-create")

	var status string
	var attempts int
	err := pool.QueryRow(ctx,
		`SELECT status, attempts FROM pull_request_enrichments WHERE pull_request_key = $1`,
		pr.Key,
	).Scan(&status, &attempts)
	if err != nil {
		t.Fatalf("enrichment row not found: %v", err)
	}
	if status != "pending" {
		t.Errorf("expected status pending, got %q", status)
	}
	if attempts != 0 {
		t.Errorf("expected 0 attempts, got %d", attempts)
	}
}

func TestSavePullRequest_ReIngestDoesNotResetEnrichment(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-reingest")

	// Manually advance enrichment to completed.
	if _, err := pool.Exec(ctx, `
		UPDATE pull_request_enrichments
		SET status = 'completed', enriched_at = NOW(), enrichment = '{"feature_flags":{"added":["f1"],"removed":[]}}'
		WHERE pull_request_key = $1
	`, pr.Key); err != nil {
		t.Fatalf("advance enrichment to completed: %v", err)
	}

	// Re-ingest the same PR.
	pr.Data.Title = "updated title"
	if err := repo.SavePullRequest(ctx, pr); err != nil {
		t.Fatalf("re-ingest SavePullRequest: %v", err)
	}

	if status := enrichmentStatus(ctx, t, pool, pr.Key); status != "completed" {
		t.Errorf("expected enrichment to remain completed after re-ingest, got %q", status)
	}
}

func TestListPendingEnrichments(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-list-pending")

	pending, err := repo.ListPendingEnrichments(ctx, orgKey, 10)
	if err != nil {
		t.Fatalf("ListPendingEnrichments: %v", err)
	}

	var found bool
	for _, e := range pending {
		if e.PullRequestKey == pr.Key {
			found = true
			if e.Status != models.PREnrichmentStatusPending {
				t.Errorf("expected pending, got %q", e.Status)
			}
		}
	}
	if !found {
		t.Fatal("newly saved PR not found in pending list")
	}
}

func TestListPendingEnrichments_ExcludesOtherOrgs(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	otherOrgKey := setupTestOrg(ctx, t, pool)
	insertTestPR(ctx, t, repo, pool, orgKey, "enrich-list-own-org")
	otherPR := insertTestPR(ctx, t, repo, pool, otherOrgKey, "enrich-list-other-org")

	pending, err := repo.ListPendingEnrichments(ctx, orgKey, 100)
	if err != nil {
		t.Fatalf("ListPendingEnrichments: %v", err)
	}
	for _, e := range pending {
		if e.OrgID != orgKey {
			t.Errorf("expected only org %s rows, got row for org %s", orgKey, e.OrgID)
		}
		if e.PullRequestKey == otherPR.Key {
			t.Error("pending row from another org should not be listed")
		}
	}
}

func TestListPendingEnrichments_IncludesStaleInProgress(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	stalePR := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-list-stale")
	freshPR := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-list-fresh")
	markInProgress(ctx, t, pool, stalePR.Key, staleUpdatedAt())
	markInProgress(ctx, t, pool, freshPR.Key, time.Now())

	pending, err := repo.ListPendingEnrichments(ctx, orgKey, 100)
	if err != nil {
		t.Fatalf("ListPendingEnrichments: %v", err)
	}

	var hasStale, hasFresh bool
	for _, e := range pending {
		hasStale = hasStale || e.PullRequestKey == stalePR.Key
		hasFresh = hasFresh || e.PullRequestKey == freshPR.Key
	}
	if !hasStale {
		t.Error("stale in_progress row should be listed for recovery")
	}
	if hasFresh {
		t.Error("recently claimed in_progress row should not be listed")
	}
}

func TestClaimEnrichment(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-claim")

	claimed, err := repo.ClaimEnrichment(ctx, pr.Key)
	if err != nil {
		t.Fatalf("ClaimEnrichment: %v", err)
	}
	if !claimed {
		t.Fatal("expected first claim to succeed")
	}

	if status := enrichmentStatus(ctx, t, pool, pr.Key); status != "in_progress" {
		t.Errorf("expected in_progress after claim, got %q", status)
	}
	if attempts := enrichmentAttempts(ctx, t, pool, pr.Key); attempts != 1 {
		t.Errorf("expected 1 attempt after claim, got %d", attempts)
	}
}

func TestClaimEnrichment_ReportsNotClaimedWhenAlreadyHeld(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-claim-twice")
	mustClaim(ctx, t, repo, pr.Key)

	claimed, err := repo.ClaimEnrichment(ctx, pr.Key)
	if err != nil {
		t.Fatalf("second ClaimEnrichment: %v", err)
	}
	if claimed {
		t.Error("expected second claim to report not claimed")
	}
	if got := enrichmentAttempts(ctx, t, pool, pr.Key); got != 1 {
		t.Errorf("expected attempts to stay 1 after a lost claim, got %d", got)
	}
}

func TestClaimEnrichment_ReportsNotClaimedWhenCompleted(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-claim-completed")
	mustClaim(ctx, t, repo, pr.Key)
	if err := repo.SaveEnrichmentResult(ctx, pr.Key, models.PullRequestEnrichmentData{}); err != nil {
		t.Fatalf("SaveEnrichmentResult: %v", err)
	}

	claimed, err := repo.ClaimEnrichment(ctx, pr.Key)
	if err != nil {
		t.Fatalf("ClaimEnrichment: %v", err)
	}
	if claimed {
		t.Error("completed row should not be claimable")
	}
}

func TestClaimEnrichment_ReclaimsStaleInProgress(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-claim-stale")
	mustClaim(ctx, t, repo, pr.Key) // attempts = 1, then the worker "dies"
	markInProgress(ctx, t, pool, pr.Key, staleUpdatedAt())

	claimed, err := repo.ClaimEnrichment(ctx, pr.Key)
	if err != nil {
		t.Fatalf("ClaimEnrichment: %v", err)
	}
	if !claimed {
		t.Fatal("stale in_progress row should be reclaimable")
	}
	if got := enrichmentAttempts(ctx, t, pool, pr.Key); got != 2 {
		t.Errorf("expected the abandoned attempt to count (attempts = 2), got %d", got)
	}
}

func TestSaveEnrichmentResult(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-save-result")
	mustClaim(ctx, t, repo, pr.Key)

	data := models.PullRequestEnrichmentData{
		FeatureFlags: models.PullRequestFeatureFlagRefs{
			Added:   []string{"flag-uuid-1"},
			Removed: []string{"flag-uuid-2"},
		},
	}
	if err := repo.SaveEnrichmentResult(ctx, pr.Key, data); err != nil {
		t.Fatalf("SaveEnrichmentResult: %v", err)
	}

	var status string
	var enrichedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, enriched_at FROM pull_request_enrichments WHERE pull_request_key = $1`,
		pr.Key,
	).Scan(&status, &enrichedAt); err != nil {
		t.Fatalf("read enrichment: %v", err)
	}

	if status != "completed" {
		t.Errorf("expected completed, got %q", status)
	}
	if enrichedAt == nil {
		t.Error("expected enriched_at to be set")
	}

	// Completed row must not appear in pending list.
	pending, _ := repo.ListPendingEnrichments(ctx, orgKey, 100)
	for _, e := range pending {
		if e.PullRequestKey == pr.Key {
			t.Error("completed row should not appear in pending list")
		}
	}
}

func TestSaveEnrichmentResult_DoesNotOverwriteUnclaimedRow(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-save-unclaimed")
	if _, err := pool.Exec(ctx,
		`UPDATE pull_request_enrichments SET status = 'failed' WHERE pull_request_key = $1`,
		pr.Key,
	); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	if err := repo.SaveEnrichmentResult(ctx, pr.Key, models.PullRequestEnrichmentData{}); err != nil {
		t.Fatalf("SaveEnrichmentResult: %v", err)
	}
	if got := enrichmentStatus(ctx, t, pool, pr.Key); got != "failed" {
		t.Errorf("save on an unclaimed row must be a no-op, status became %q", got)
	}
}

func TestMarkEnrichmentFailed_BelowCap(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-fail-below-cap")
	mustClaim(ctx, t, repo, pr.Key) // attempts = 1

	if err := repo.MarkEnrichmentFailed(ctx, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed: %v", err)
	}

	if status := enrichmentStatus(ctx, t, pool, pr.Key); status != "pending" {
		t.Errorf("expected pending after first failure, got %q", status)
	}
}

func TestMarkEnrichmentFailed_CapReached(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-fail-cap")

	// Simulate hitting the cap on a claimed row by setting attempts directly.
	if _, err := pool.Exec(ctx,
		`UPDATE pull_request_enrichments SET status = 'in_progress', attempts = $2 WHERE pull_request_key = $1`,
		pr.Key, enrichmentMaxAttempts,
	); err != nil {
		t.Fatalf("set attempts: %v", err)
	}

	if err := repo.MarkEnrichmentFailed(ctx, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed at cap: %v", err)
	}

	if status := enrichmentStatus(ctx, t, pool, pr.Key); status != "failed" {
		t.Errorf("expected failed when cap reached, got %q", status)
	}
}

func TestMarkEnrichmentFailed_DoesNotResetCompletedRow(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-fail-completed")
	mustClaim(ctx, t, repo, pr.Key)
	if err := repo.SaveEnrichmentResult(ctx, pr.Key, models.PullRequestEnrichmentData{}); err != nil {
		t.Fatalf("SaveEnrichmentResult: %v", err)
	}

	if err := repo.MarkEnrichmentFailed(ctx, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed: %v", err)
	}
	if got := enrichmentStatus(ctx, t, pool, pr.Key); got != "completed" {
		t.Errorf("mark-failed on a completed row must be a no-op, status became %q", got)
	}
}

// mustClaim claims a row and fails the test if it was not claimable.
func mustClaim(ctx context.Context, t *testing.T, repo *PostgresRepository, pullRequestKey uuid.UUID) {
	t.Helper()
	claimed, err := repo.ClaimEnrichment(ctx, pullRequestKey)
	if err != nil {
		t.Fatalf("ClaimEnrichment: %v", err)
	}
	if !claimed {
		t.Fatal("expected row to be claimable")
	}
}

// staleUpdatedAt returns a timestamp old enough for an in_progress claim to count as abandoned.
func staleUpdatedAt() time.Time {
	return time.Now().Add(-2 * enrichmentStaleAfterSeconds * time.Second)
}

// markInProgress puts a row in_progress with the given updated_at.
func markInProgress(ctx context.Context, t *testing.T, pool *pgxpool.Pool, pullRequestKey uuid.UUID, updatedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE pull_request_enrichments SET status = 'in_progress', updated_at = $2 WHERE pull_request_key = $1`,
		pullRequestKey, updatedAt,
	); err != nil {
		t.Fatalf("mark in_progress: %v", err)
	}
}

func enrichmentStatus(ctx context.Context, t *testing.T, pool *pgxpool.Pool, pullRequestKey uuid.UUID) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM pull_request_enrichments WHERE pull_request_key = $1`, pullRequestKey,
	).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}

func enrichmentAttempts(ctx context.Context, t *testing.T, pool *pgxpool.Pool, pullRequestKey uuid.UUID) int {
	t.Helper()
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT attempts FROM pull_request_enrichments WHERE pull_request_key = $1`, pullRequestKey,
	).Scan(&attempts); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	return attempts
}

// newTestRepo creates a PostgresRepository and a raw pool for direct assertions.
func newTestRepo(t *testing.T, ctx context.Context) (*PostgresRepository, *pgxpool.Pool) {
	t.Helper()
	repo, err := NewPostgresRepository(ctx, testConnString, testEncryptionKey)
	if err != nil {
		t.Fatalf("NewPostgresRepository: %v", err)
	}
	t.Cleanup(repo.Close)

	pool, err := pgxpool.New(ctx, testConnString)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	return repo, pool
}

// insertTestPR saves a minimal PullRequest and returns it.
func insertTestPR(ctx context.Context, t *testing.T, repo *PostgresRepository, pool *pgxpool.Pool, orgKey uuid.UUID, prID string) models.PullRequest {
	t.Helper()

	integrationKey := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, 'github', $3, '{}'::jsonb, 'active', NOW(), NOW())
	`, integrationKey, orgKey, mustEncryptTestAPIKey(t, "")); err != nil {
		t.Fatalf("insert integration: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	pr := models.PullRequest{
		Key:             models.ComputePullRequestKey(orgKey, prID),
		SourceRecordKey: uuid.New(),
		IntegrationID:   integrationKey,
		OrgID:           orgKey,
		State:           models.PullRequestStateMerged,
		Data:            models.PullRequestData{Title: prID},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := repo.SavePullRequest(ctx, pr); err != nil {
		t.Fatalf("SavePullRequest: %v", err)
	}
	return pr
}
