//go:build integration

package repository

import (
	"context"
	"fmt"
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

// PRs of a paused or deleted integration must not be listed, so nothing calls GitHub for
// them. They stay pending and are listed again once the integration is active.
func TestListPendingEnrichments_SkipsInactiveIntegrations(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	activePR := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-list-active-integration")
	pausedPR := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-list-paused-integration")
	deletedPR := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-list-deleted-integration")
	setIntegrationState(ctx, t, pool, pausedPR.IntegrationID, models.IntegrationStatePaused)
	setIntegrationState(ctx, t, pool, deletedPR.IntegrationID, models.IntegrationStateDeleted)

	listed := listedPRKeys(ctx, t, repo, orgKey)
	if !listed[activePR.Key] {
		t.Error("PR of an active integration should be listed")
	}
	if listed[pausedPR.Key] {
		t.Error("PR of a paused integration should not be listed")
	}
	if listed[deletedPR.Key] {
		t.Error("PR of a deleted integration should not be listed")
	}
	if status := enrichmentStatus(ctx, t, pool, pausedPR.Key); status != "pending" {
		t.Errorf("paused integration's PR should stay pending, got %q", status)
	}

	setIntegrationState(ctx, t, pool, pausedPR.IntegrationID, models.IntegrationStateActive)
	if !listedPRKeys(ctx, t, repo, orgKey)[pausedPR.Key] {
		t.Error("PR should be listed again once its integration is resumed")
	}
}

func TestClaimEnrichment(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-claim")

	claimed, err := repo.ClaimEnrichment(ctx, pr.OrgID, pr.Key)
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
	mustClaim(ctx, t, repo, pr.OrgID, pr.Key)

	claimed, err := repo.ClaimEnrichment(ctx, pr.OrgID, pr.Key)
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
	mustClaim(ctx, t, repo, pr.OrgID, pr.Key)
	if err := repo.SaveEnrichmentResult(ctx, pr.OrgID, pr.Key, models.PullRequestEnrichmentData{}); err != nil {
		t.Fatalf("SaveEnrichmentResult: %v", err)
	}

	claimed, err := repo.ClaimEnrichment(ctx, pr.OrgID, pr.Key)
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
	mustClaim(ctx, t, repo, pr.OrgID, pr.Key) // attempts = 1, then the worker "dies"
	markInProgress(ctx, t, pool, pr.Key, staleUpdatedAt())

	claimed, err := repo.ClaimEnrichment(ctx, pr.OrgID, pr.Key)
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
	mustClaim(ctx, t, repo, pr.OrgID, pr.Key)

	data := models.PullRequestEnrichmentData{
		FeatureFlags: models.PullRequestFeatureFlagRefs{
			Added:   []string{"flag-uuid-1"},
			Removed: []string{"flag-uuid-2"},
		},
	}
	if err := repo.SaveEnrichmentResult(ctx, pr.OrgID, pr.Key, data); err != nil {
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

	if err := repo.SaveEnrichmentResult(ctx, pr.OrgID, pr.Key, models.PullRequestEnrichmentData{}); err != nil {
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
	mustClaim(ctx, t, repo, pr.OrgID, pr.Key) // attempts = 1

	if err := repo.MarkEnrichmentFailed(ctx, pr.OrgID, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed: %v", err)
	}

	if status := enrichmentStatus(ctx, t, pool, pr.Key); status != "pending" {
		t.Errorf("expected pending after first failure, got %q", status)
	}
}

// Each failure pushes the next attempt further out: 30m, doubling, capped at 24h.
func TestMarkEnrichmentFailed_BacksOff(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)
	orgKey := setupTestOrg(ctx, t, pool)

	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{1, 30 * time.Minute},
		{2, time.Hour},
		{3, 2 * time.Hour},
		{4, 4 * time.Hour},
		{5, 8 * time.Hour},
		{6, 16 * time.Hour},
		{7, 24 * time.Hour},
		{9, 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("after %d attempts", tt.attempts), func(t *testing.T) {
			pr := insertTestPR(ctx, t, repo, pool, orgKey, fmt.Sprintf("enrich-backoff-%d", tt.attempts))
			if _, err := pool.Exec(ctx,
				`UPDATE pull_request_enrichments SET status = 'in_progress', attempts = $2 WHERE pull_request_key = $1`,
				pr.Key, tt.attempts,
			); err != nil {
				t.Fatalf("set attempts: %v", err)
			}

			if err := repo.MarkEnrichmentFailed(ctx, pr.OrgID, pr.Key); err != nil {
				t.Fatalf("MarkEnrichmentFailed: %v", err)
			}

			if status := enrichmentStatus(ctx, t, pool, pr.Key); status != "pending" {
				t.Fatalf("expected pending below the cap, got %q", status)
			}
			if got := nextAttemptIn(ctx, t, pool, pr.Key); got < tt.want-time.Minute || got > tt.want {
				t.Errorf("next attempt in %v, want about %v", got, tt.want)
			}
		})
	}
}

// A PR waiting out its backoff is neither listed nor claimable; once the wait has passed
// it is both.
func TestEnrichmentBackoff_HidesRowUntilNextAttempt(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-backoff-hidden")
	mustClaim(ctx, t, repo, pr.OrgID, pr.Key)
	if err := repo.MarkEnrichmentFailed(ctx, pr.OrgID, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed: %v", err)
	}

	if listedPRKeys(ctx, t, repo, orgKey)[pr.Key] {
		t.Error("row in backoff should not be listed")
	}
	claimed, err := repo.ClaimEnrichment(ctx, pr.OrgID, pr.Key)
	if err != nil {
		t.Fatalf("ClaimEnrichment: %v", err)
	}
	if claimed {
		t.Fatal("row in backoff should not be claimable")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE pull_request_enrichments SET next_attempt_at = NOW() - INTERVAL '1 second' WHERE pull_request_key = $1`,
		pr.Key,
	); err != nil {
		t.Fatalf("expire backoff: %v", err)
	}
	if !listedPRKeys(ctx, t, repo, orgKey)[pr.Key] {
		t.Error("row should be listed once its backoff has passed")
	}
	mustClaim(ctx, t, repo, pr.OrgID, pr.Key)
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

	if err := repo.MarkEnrichmentFailed(ctx, pr.OrgID, pr.Key); err != nil {
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
	mustClaim(ctx, t, repo, pr.OrgID, pr.Key)
	if err := repo.SaveEnrichmentResult(ctx, pr.OrgID, pr.Key, models.PullRequestEnrichmentData{}); err != nil {
		t.Fatalf("SaveEnrichmentResult: %v", err)
	}

	if err := repo.MarkEnrichmentFailed(ctx, pr.OrgID, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed: %v", err)
	}
	if got := enrichmentStatus(ctx, t, pool, pr.Key); got != "completed" {
		t.Errorf("mark-failed on a completed row must be a no-op, status became %q", got)
	}
}

// mustClaim claims a row and fails the test if it was not claimable.
func mustClaim(ctx context.Context, t *testing.T, repo *PostgresRepository, orgID, pullRequestKey uuid.UUID) {
	t.Helper()
	claimed, err := repo.ClaimEnrichment(ctx, orgID, pullRequestKey)
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

func setIntegrationState(ctx context.Context, t *testing.T, pool *pgxpool.Pool, integrationKey uuid.UUID, state models.IntegrationState) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE integrations SET state = $2 WHERE key = $1`, integrationKey, state); err != nil {
		t.Fatalf("set integration state: %v", err)
	}
}

func listedPRKeys(ctx context.Context, t *testing.T, repo *PostgresRepository, orgKey uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	pending, err := repo.ListPendingEnrichments(ctx, orgKey, 100)
	if err != nil {
		t.Fatalf("ListPendingEnrichments: %v", err)
	}
	keys := make(map[uuid.UUID]bool, len(pending))
	for _, e := range pending {
		keys[e.PullRequestKey] = true
	}
	return keys
}

// nextAttemptIn returns how far in the future the row's next attempt is, per the DB clock.
func nextAttemptIn(ctx context.Context, t *testing.T, pool *pgxpool.Pool, pullRequestKey uuid.UUID) time.Duration {
	t.Helper()
	var seconds float64
	if err := pool.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM next_attempt_at - NOW()) FROM pull_request_enrichments WHERE pull_request_key = $1`,
		pullRequestKey,
	).Scan(&seconds); err != nil {
		t.Fatalf("read next_attempt_at: %v", err)
	}
	return time.Duration(seconds * float64(time.Second))
}

// Invariant 1: a key alone must never reach another tenant's row. Every enrichment write
// and the PR lookup are scoped by org_id, so the wrong org changes and reads nothing.
func TestEnrichmentQueries_AreOrgScoped(t *testing.T) {
	ctx := context.Background()
	repo, pool := newTestRepo(t, ctx)

	orgKey := setupTestOrg(ctx, t, pool)
	otherOrg := setupTestOrg(ctx, t, pool)
	pr := insertTestPR(ctx, t, repo, pool, orgKey, "enrich-org-scope")

	claimed, err := repo.ClaimEnrichment(ctx, otherOrg, pr.Key)
	if err != nil {
		t.Fatalf("ClaimEnrichment: %v", err)
	}
	if claimed {
		t.Fatal("another org claimed this org's enrichment row")
	}
	if got := enrichmentAttempts(ctx, t, pool, pr.Key); got != 0 {
		t.Errorf("attempts = %d after a foreign claim, want 0", got)
	}

	if _, err := repo.GetPullRequestByKey(ctx, otherOrg, pr.Key); err == nil {
		t.Error("GetPullRequestByKey returned another org's pull request")
	}
	if got, err := repo.GetPullRequestByKey(ctx, orgKey, pr.Key); err != nil || got.Key != pr.Key {
		t.Errorf("GetPullRequestByKey for the owning org: key=%s err=%v", got.Key, err)
	}

	mustClaim(ctx, t, repo, orgKey, pr.Key)
	if err := repo.SaveEnrichmentResult(ctx, otherOrg, pr.Key, models.PullRequestEnrichmentData{}); err != nil {
		t.Fatalf("SaveEnrichmentResult: %v", err)
	}
	if err := repo.MarkEnrichmentFailed(ctx, otherOrg, pr.Key); err != nil {
		t.Fatalf("MarkEnrichmentFailed: %v", err)
	}
	if got := enrichmentStatus(ctx, t, pool, pr.Key); got != "in_progress" {
		t.Errorf("status = %q after foreign save and mark-failed, want in_progress", got)
	}
}
