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
	pool.Exec(ctx, `
		UPDATE pull_request_enrichments
		SET status = 'completed', enriched_at = NOW(), enrichment = '{"feature_flags":{"added":["f1"],"removed":[]}}'
		WHERE pull_request_key = $1
	`, pr.Key)

	// Re-ingest the same PR.
	pr.Data.Title = "updated title"
	if err := repo.SavePullRequest(ctx, pr); err != nil {
		t.Fatalf("re-ingest SavePullRequest: %v", err)
	}

	var status string
	pool.QueryRow(ctx,
		`SELECT status FROM pull_request_enrichments WHERE pull_request_key = $1`,
		pr.Key,
	).Scan(&status)
	if status != "completed" {
		t.Errorf("expected enrichment to remain completed after re-ingest, got %q", status)
	}
}

func TestListPendingEnrichments(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-list-pending")

	pending, err := repo.ListPendingEnrichments(ctx, 10)
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

func TestClaimEnrichment(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-claim")

	if err := repo.ClaimEnrichment(ctx, pr.Key); err != nil {
		t.Fatalf("ClaimEnrichment: %v", err)
	}

	var status string
	var attempts int
	pool.QueryRow(ctx,
		`SELECT status, attempts FROM pull_request_enrichments WHERE pull_request_key = $1`,
		pr.Key,
	).Scan(&status, &attempts)

	if status != "in_progress" {
		t.Errorf("expected in_progress after claim, got %q", status)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt after claim, got %d", attempts)
	}
}

func TestSaveEnrichmentResult(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-save-result")
	repo.ClaimEnrichment(ctx, pr.Key)

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
	pool.QueryRow(ctx,
		`SELECT status, enriched_at FROM pull_request_enrichments WHERE pull_request_key = $1`,
		pr.Key,
	).Scan(&status, &enrichedAt)

	if status != "completed" {
		t.Errorf("expected completed, got %q", status)
	}
	if enrichedAt == nil {
		t.Error("expected enriched_at to be set")
	}

	// Completed row must not appear in pending list.
	pending, _ := repo.ListPendingEnrichments(ctx, 100)
	for _, e := range pending {
		if e.PullRequestKey == pr.Key {
			t.Error("completed row should not appear in pending list")
		}
	}
}

func TestMarkEnrichmentFailed_BelowCap(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-fail-below-cap")
	repo.ClaimEnrichment(ctx, pr.Key) // attempts = 1

	if err := repo.MarkEnrichmentFailed(ctx, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed: %v", err)
	}

	var status string
	pool.QueryRow(ctx,
		`SELECT status FROM pull_request_enrichments WHERE pull_request_key = $1`,
		pr.Key,
	).Scan(&status)
	if status != "pending" {
		t.Errorf("expected pending after first failure, got %q", status)
	}
}

func TestMarkEnrichmentFailed_CapReached(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-fail-cap")

	// Simulate hitting the cap by setting attempts = enrichmentMaxAttempts directly.
	pool.Exec(ctx,
		`UPDATE pull_request_enrichments SET attempts = $2 WHERE pull_request_key = $1`,
		pr.Key, enrichmentMaxAttempts,
	)

	if err := repo.MarkEnrichmentFailed(ctx, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed at cap: %v", err)
	}

	var status string
	pool.QueryRow(ctx,
		`SELECT status FROM pull_request_enrichments WHERE pull_request_key = $1`,
		pr.Key,
	).Scan(&status)
	if status != "failed" {
		t.Errorf("expected failed when cap reached, got %q", status)
	}
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
