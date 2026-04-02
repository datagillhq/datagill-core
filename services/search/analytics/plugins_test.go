package analytics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/repository"
	"github.com/datagill/datagill-core/services/search/analytics"
)

// stubAnalyticsRepo implements AnalyticsRepository for testing.
type stubAnalyticsRepo struct {
	launchedWins repository.LaunchedWinsResult
	topTeams     repository.TopTeamsResult
	velocity     repository.VelocityResult
	auditLog     repository.AuditLogResult
	err          error
}

func (s *stubAnalyticsRepo) QueryLaunchedWins(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string) (repository.LaunchedWinsResult, error) {
	return s.launchedWins, s.err
}
func (s *stubAnalyticsRepo) QueryTopTeams(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.TopTeamsResult, error) {
	return s.topTeams, s.err
}
func (s *stubAnalyticsRepo) QueryVelocity(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.VelocityResult, error) {
	return s.velocity, s.err
}
func (s *stubAnalyticsRepo) QueryAuditLog(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string, _, _ int) (repository.AuditLogResult, error) {
	return s.auditLog, s.err
}

var testQuery = analytics.Query{
	OrgID: uuid.New(),
	From:  time.Now().Add(-24 * time.Hour),
	To:    time.Now(),
	Page:  1,
	Limit: 25,
}

func TestLaunchedWinsPlugin(t *testing.T) {
	flagKey := uuid.New()
	now := time.Now()
	repo := &stubAnalyticsRepo{launchedWins: repository.LaunchedWinsResult{
		Wins: []repository.LaunchedWin{
			{FlagKey: flagKey, FlagName: "my-flag", ActorEmail: "a@b.com", ActorName: "Alice", OccurredAt: now},
		},
	}}
	p := analytics.NewLaunchedWinsPlugin(repo)

	if p.Metric() != "launched_wins" {
		t.Fatalf("unexpected metric name: %s", p.Metric())
	}
	out, err := p.Execute(context.Background(), testQuery)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := out.([]analytics.LaunchedWinEntry)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].FlagKey != flagKey || entries[0].RolloutPercentage != 100 {
		t.Errorf("unexpected entry: %+v", entries[0])
	}

	// Empty result returns empty slice, not nil.
	repo.launchedWins = repository.LaunchedWinsResult{}
	out, err = p.Execute(context.Background(), testQuery)
	if err != nil || len(out.([]analytics.LaunchedWinEntry)) != 0 {
		t.Errorf("empty result: err=%v out=%v", err, out)
	}

	// Repo error propagates.
	repo.err = errors.New("db error")
	if _, err := p.Execute(context.Background(), testQuery); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestTopTeamsPlugin(t *testing.T) {
	repo := &stubAnalyticsRepo{topTeams: repository.TopTeamsResult{
		Teams: []repository.TopTeam{
			{TeamName: "alpha", ChangeCount: 5},
			{TeamName: "beta", ChangeCount: 2},
		},
	}}
	p := analytics.NewTopTeamsPlugin(repo)

	if p.Metric() != "top_teams" {
		t.Fatalf("unexpected metric name: %s", p.Metric())
	}
	out, err := p.Execute(context.Background(), testQuery)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := out.([]analytics.TopTeamEntry)
	if len(entries) != 2 || entries[0].TeamName != "alpha" || entries[0].ChangeCount != 5 {
		t.Errorf("unexpected entries: %+v", entries)
	}

	// Empty result.
	repo.topTeams = repository.TopTeamsResult{}
	out, _ = p.Execute(context.Background(), testQuery)
	if len(out.([]analytics.TopTeamEntry)) != 0 {
		t.Error("expected empty slice")
	}
}

func TestVelocityPlugin(t *testing.T) {
	repo := &stubAnalyticsRepo{velocity: repository.VelocityResult{AverageMinutes: 90.5, SampleSize: 4}}
	p := analytics.NewVelocityPlugin(repo)

	if p.Metric() != "velocity" {
		t.Fatalf("unexpected metric name: %s", p.Metric())
	}
	out, err := p.Execute(context.Background(), testQuery)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v := out.(analytics.VelocityEntry)
	if v.AverageTimeToFullRolloutMinutes != 90.5 || v.SampleSize != 4 {
		t.Errorf("unexpected velocity: %+v", v)
	}

	// SampleSize < 3 — backend still returns it; frontend owns the disclaimer.
	repo.velocity = repository.VelocityResult{AverageMinutes: 60, SampleSize: 2}
	out, _ = p.Execute(context.Background(), testQuery)
	if out.(analytics.VelocityEntry).SampleSize != 2 {
		t.Error("expected SampleSize 2 to pass through")
	}
}

func TestAuditLogPlugin(t *testing.T) {
	flagKey := uuid.New()
	now := time.Now()
	repo := &stubAnalyticsRepo{auditLog: repository.AuditLogResult{
		Total: 1,
		Entries: []repository.AuditEntry{
			{OccurredAt: now, FlagName: "f", FlagKey: flagKey, ActorEmail: "a@b.com", EventType: "updated", Environments: []string{"prod"}},
		},
	}}
	q := testQuery
	q.Page = 1
	p := analytics.NewAuditLogPlugin(repo)

	if p.Metric() != "audit_log" {
		t.Fatalf("unexpected metric name: %s", p.Metric())
	}
	out, err := p.Execute(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := out.(analytics.AuditLogResponse)
	if resp.Total != 1 || resp.Count != 1 || resp.Page != 1 {
		t.Errorf("unexpected response shape: %+v", resp)
	}
	if resp.Data[0].FlagKey != flagKey {
		t.Errorf("unexpected flag key: %v", resp.Data[0].FlagKey)
	}

	// nil environments coerced to empty slice.
	repo.auditLog.Entries[0].Environments = nil
	out, _ = p.Execute(context.Background(), q)
	if out.(analytics.AuditLogResponse).Data[0].Environments == nil {
		t.Error("expected non-nil environments slice")
	}
}
