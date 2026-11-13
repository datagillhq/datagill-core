//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/datagill/datagill-core/models"
)

// setupTestFlag inserts a feature_flag with the given environments JSONB and returns its key.
func setupTestFlag(ctx context.Context, t *testing.T, pool *pgxpool.Pool, orgID, integrationID uuid.UUID, envs map[string]models.FlagEnvironment, teamName string) uuid.UUID {
	t.Helper()
	key := uuid.New()
	maintainer := map[string]any{}
	if teamName != "" {
		maintainer["team"] = map[string]string{"name": teamName}
	}
	data := map[string]any{
		"name":         "flag-" + key.String()[:8],
		"source":       map[string]any{"key": key.String(), "provider": "launchdarkly", "project": map[string]string{"id": "proj"}},
		"lifecycle":    map[string]string{"purpose": "temporary", "state": "active"},
		"environments": envs,
		"maintainer":   maintainer,
	}
	dataJSON, _ := json.Marshal(data)
	_, err := pool.Exec(ctx, `
		INSERT INTO feature_flags (key, source_record_key, integration_id, org_id, state, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'on', $5, NOW() - INTERVAL '2 hours', NOW())
	`, key, uuid.New(), integrationID, orgID, dataJSON)
	if err != nil {
		t.Fatalf("setupTestFlag: %v", err)
	}
	return key
}

// setupTestChangelog inserts a changelog entry for the given flag and returns its key.
func setupTestChangelog(ctx context.Context, t *testing.T, pool *pgxpool.Pool, orgID, integrationID, flagKey uuid.UUID, occurredAt time.Time, actorEmail, actorName string, envs []string, titleVerb, description string) {
	t.Helper()
	sourceRecordKey := uuid.New()
	data := map[string]any{
		"actor":        map[string]string{"email": actorEmail, "name": actorName},
		"source":       map[string]any{"key": flagKey.String(), "provider": "launchdarkly", "project": map[string]string{"id": "proj"}},
		"environments": envs,
		"change_type":  "rollout",
	}
	if description != "" {
		data["description"] = description
	}
	dataJSON, _ := json.Marshal(data)
	rawJSON, _ := json.Marshal(map[string]any{
		"_id":       uuid.NewString(),
		"titleVerb": titleVerb,
	})
	_, err := pool.Exec(ctx, `
		INSERT INTO source_records (key, integration_id, org_id, source_type, data, created_at)
		VALUES ($1, $2, $3, 'audit_log', $4, NOW())
	`, sourceRecordKey, integrationID, orgID, rawJSON)
	if err != nil {
		t.Fatalf("setupTestSourceRecord: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO feature_flag_changelog
		  (key, source_record_key, feature_flag_key, integration_id, org_id, event_type, state, occurred_at, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'updated', 'active', $6, $7, NOW(), NOW())
	`, uuid.New(), sourceRecordKey, flagKey, integrationID, orgID, occurredAt, dataJSON)
	if err != nil {
		t.Fatalf("setupTestChangelog: %v", err)
	}
}

func TestQueryLaunchedWins(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString, testEncryptionKey)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	intID := setupTestIntegration(ctx, t, pool, orgID)
	now := time.Now().UTC()
	window := [2]time.Time{now.Add(-1 * time.Hour), now.Add(1 * time.Hour)}

	// Flag launched to 100% in production with later off event — should still appear once.
	flagAt100 := setupTestFlag(ctx, t, pool, orgID, intID,
		map[string]models.FlagEnvironment{"production": {Enabled: true, RolloutPercentage: ptr(100.0)}}, "team-alpha")
	setupTestChangelog(ctx, t, pool, orgID, intID, flagAt100, now.Add(-10*time.Minute), "alice@example.com", "Alice", []string{"production"}, "turned on the flag", "")
	setupTestChangelog(ctx, t, pool, orgID, intID, flagAt100, now, "alice@example.com", "Alice", []string{"production"}, "turned off the flag", "")

	// Flag at 50% rollout — should appear with its rollout percentage.
	flag50 := setupTestFlag(ctx, t, pool, orgID, intID,
		map[string]models.FlagEnvironment{"production": {Enabled: true, RolloutPercentage: ptr(50.0)}}, "")
	setupTestChangelog(ctx, t, pool, orgID, intID, flag50, now, "bob@example.com", "Bob", []string{"production"}, "", "* Set the default rollout to `50%`")

	// Duplicate qualifying events for the same flag — should dedupe to the most recent one.
	flagDeduped := setupTestFlag(ctx, t, pool, orgID, intID,
		map[string]models.FlagEnvironment{"production": {Enabled: true, RolloutPercentage: ptr(25.0)}}, "")
	setupTestChangelog(ctx, t, pool, orgID, intID, flagDeduped, now.Add(-20*time.Minute), "cara@example.com", "Cara", []string{"production"}, "", "* Set the default rollout to `1%`")
	setupTestChangelog(ctx, t, pool, orgID, intID, flagDeduped, now.Add(-5*time.Minute), "cara@example.com", "Cara", []string{"production"}, "", "* Changed the default rollout from `1%` to `25%`")

	// Non-production rollout should not appear.
	flagStagingOnly := setupTestFlag(ctx, t, pool, orgID, intID,
		map[string]models.FlagEnvironment{"staging": {Enabled: true, RolloutPercentage: ptr(80.0)}}, "")
	setupTestChangelog(ctx, t, pool, orgID, intID, flagStagingOnly, now, "dina@example.com", "Dina", []string{"staging"}, "", "* Set the default rollout to `80%`")

	res, err := repo.QueryLaunchedWins(ctx, orgID, window[0], window[1], "", "")
	if err != nil {
		t.Fatalf("QueryLaunchedWins: %v", err)
	}
	if len(res.Wins) != 3 {
		t.Fatalf("expected 3 wins, got %d", len(res.Wins))
	}
	if res.Wins[0].FlagKey != flag50 || res.Wins[0].ActorEmail != "bob@example.com" || res.Wins[0].RolloutPercentage != 50 {
		t.Errorf("expected latest row to be 50%% rollout flag, got %+v", res.Wins[0])
	}
	if res.Wins[1].FlagKey != flagDeduped || res.Wins[1].ActorEmail != "cara@example.com" || res.Wins[1].RolloutPercentage != 25 {
		t.Errorf("expected second row to be deduped 25%% rollout flag, got %+v", res.Wins[1])
	}
	if res.Wins[2].FlagKey != flagAt100 || res.Wins[2].ActorEmail != "alice@example.com" || res.Wins[2].RolloutPercentage != 100 {
		t.Errorf("expected rolled-back 100%% launch to remain in results, got %+v", res.Wins[2])
	}

	// Team filter: "team-alpha" returns result; "team-beta" does not.
	res, err = repo.QueryLaunchedWins(ctx, orgID, window[0], window[1], "team-alpha", "")
	if err != nil || len(res.Wins) != 1 || res.Wins[0].FlagKey != flagAt100 {
		t.Errorf("team filter team-alpha: err=%v, count=%d", err, len(res.Wins))
	}
	res, err = repo.QueryLaunchedWins(ctx, orgID, window[0], window[1], "team-beta", "")
	if err != nil || len(res.Wins) != 0 {
		t.Errorf("team filter team-beta: err=%v, count=%d", err, len(res.Wins))
	}

	// Actor filter narrows to the latest qualifying production event for that actor.
	res, err = repo.QueryLaunchedWins(ctx, orgID, window[0], window[1], "", "cara@example.com")
	if err != nil || len(res.Wins) != 1 || res.Wins[0].FlagKey != flagDeduped || res.Wins[0].RolloutPercentage != 25 {
		t.Errorf("actor filter cara@example.com: err=%v, wins=%+v", err, res.Wins)
	}
}

func TestQueryTopTeams(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString, testEncryptionKey)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	intID := setupTestIntegration(ctx, t, pool, orgID)
	now := time.Now().UTC()
	window := [2]time.Time{now.Add(-1 * time.Hour), now.Add(1 * time.Hour)}

	flag1 := setupTestFlag(ctx, t, pool, orgID, intID, map[string]models.FlagEnvironment{}, "alpha")
	flag2 := setupTestFlag(ctx, t, pool, orgID, intID, map[string]models.FlagEnvironment{}, "alpha")
	flag3 := setupTestFlag(ctx, t, pool, orgID, intID, map[string]models.FlagEnvironment{}, "beta")
	// No team — should not appear.
	flag4 := setupTestFlag(ctx, t, pool, orgID, intID, map[string]models.FlagEnvironment{}, "")

	for _, fk := range []uuid.UUID{flag1, flag2} {
		setupTestChangelog(ctx, t, pool, orgID, intID, fk, now, "a@a.com", "", nil, "", "")
	}
	setupTestChangelog(ctx, t, pool, orgID, intID, flag3, now, "b@b.com", "", nil, "", "")
	setupTestChangelog(ctx, t, pool, orgID, intID, flag4, now, "c@c.com", "", nil, "", "")

	res, err := repo.QueryTopTeams(ctx, orgID, window[0], window[1])
	if err != nil {
		t.Fatalf("QueryTopTeams: %v", err)
	}
	if len(res.Teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(res.Teams))
	}
	if res.Teams[0].TeamName != "alpha" || res.Teams[0].ChangeCount != 2 {
		t.Errorf("top team: got %+v", res.Teams[0])
	}
	if res.Teams[1].TeamName != "beta" || res.Teams[1].ChangeCount != 1 {
		t.Errorf("second team: got %+v", res.Teams[1])
	}
}

func TestQueryVelocity(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString, testEncryptionKey)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	intID := setupTestIntegration(ctx, t, pool, orgID)
	now := time.Now().UTC()
	window := [2]time.Time{now.Add(-30 * time.Minute), now.Add(30 * time.Minute)}

	// Flag created 2h ago (by setupTestFlag), changelog event now — ~120 min velocity.
	flag := setupTestFlag(ctx, t, pool, orgID, intID,
		map[string]models.FlagEnvironment{"prod": {Enabled: true, RolloutPercentage: ptr(100.0)}}, "")
	setupTestChangelog(ctx, t, pool, orgID, intID, flag, now, "a@a.com", "", nil, "", "")

	res, err := repo.QueryVelocity(ctx, orgID, window[0], window[1])
	if err != nil {
		t.Fatalf("QueryVelocity: %v", err)
	}
	if res.SampleSize != 1 {
		t.Errorf("expected SampleSize 1, got %d", res.SampleSize)
	}
	// Flag created 2h ago — expect ~120 minutes; allow 30-minute buffer for timing jitter.
	if res.AverageMinutes < 90 || res.AverageMinutes > 150 {
		t.Errorf("unexpected AverageMinutes: %f", res.AverageMinutes)
	}

	// Empty window — SampleSize = 0, AverageMinutes = 0.
	res, err = repo.QueryVelocity(ctx, orgID, now.Add(-2*time.Hour), now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("QueryVelocity (empty): %v", err)
	}
	if res.SampleSize != 0 || res.AverageMinutes != 0 {
		t.Errorf("expected empty result, got %+v", res)
	}
}

func TestQueryAuditLog(t *testing.T) {
	ctx := context.Background()
	repo, _ := NewPostgresRepository(ctx, testConnString, testEncryptionKey)
	defer repo.Close()
	pool, _ := pgxpool.New(ctx, testConnString)
	defer pool.Close()

	orgID := setupTestOrg(ctx, t, pool)
	intID := setupTestIntegration(ctx, t, pool, orgID)
	now := time.Now().UTC()
	window := [2]time.Time{now.Add(-1 * time.Hour), now.Add(1 * time.Hour)}

	flag := setupTestFlag(ctx, t, pool, orgID, intID, map[string]models.FlagEnvironment{}, "team-x")
	setupTestChangelog(ctx, t, pool, orgID, intID, flag, now, "alice@example.com", "Alice", []string{"staging", "production"}, "", "")
	setupTestChangelog(ctx, t, pool, orgID, intID, flag, now.Add(-10*time.Minute), "bob@example.com", "Bob", []string{"staging"}, "", "")

	// Unfiltered — 2 results, total = 2.
	res, err := repo.QueryAuditLog(ctx, orgID, window[0], window[1], "", "", 1, 25)
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if res.Total != 2 || len(res.Entries) != 2 {
		t.Fatalf("expected total=2 entries=2, got total=%d entries=%d", res.Total, len(res.Entries))
	}
	if res.Entries[0].ActorEmail != "alice@example.com" {
		t.Errorf("expected alice first (most recent), got %s", res.Entries[0].ActorEmail)
	}
	if len(res.Entries[0].Environments) != 2 {
		t.Errorf("expected 2 environments, got %v", res.Entries[0].Environments)
	}

	// Actor filter.
	res, err = repo.QueryAuditLog(ctx, orgID, window[0], window[1], "", "bob@example.com", 1, 25)
	if err != nil || res.Total != 1 {
		t.Errorf("actor filter: err=%v total=%d", err, res.Total)
	}

	// Team filter + both filters active.
	res, err = repo.QueryAuditLog(ctx, orgID, window[0], window[1], "team-x", "alice@example.com", 1, 25)
	if err != nil || res.Total != 1 {
		t.Errorf("team+actor filter: err=%v total=%d", err, res.Total)
	}

	// Pagination: page 1 limit 1 → 1 entry, total still 2.
	res, err = repo.QueryAuditLog(ctx, orgID, window[0], window[1], "", "", 1, 1)
	if err != nil || res.Total != 2 || len(res.Entries) != 1 {
		t.Errorf("pagination: err=%v total=%d entries=%d", err, res.Total, len(res.Entries))
	}

	// Page past the end: no entries, but the total is still the real count.
	res, err = repo.QueryAuditLog(ctx, orgID, window[0], window[1], "", "", 5, 1)
	if err != nil || res.Total != 2 || len(res.Entries) != 0 {
		t.Errorf("page past the end: err=%v total=%d entries=%d, want total=2 entries=0", err, res.Total, len(res.Entries))
	}
}

func ptr(f float64) *float64 { return &f }
