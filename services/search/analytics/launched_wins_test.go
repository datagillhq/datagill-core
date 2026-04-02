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

type stubLaunchedWinsRepo struct {
	result repository.LaunchedWinsResult
	err    error
}

func (s *stubLaunchedWinsRepo) QueryLaunchedWins(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string) (repository.LaunchedWinsResult, error) {
	return s.result, s.err
}
func (s *stubLaunchedWinsRepo) QueryTopTeams(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.TopTeamsResult, error) {
	return repository.TopTeamsResult{}, nil
}
func (s *stubLaunchedWinsRepo) QueryVelocity(_ context.Context, _ uuid.UUID, _, _ time.Time) (repository.VelocityResult, error) {
	return repository.VelocityResult{}, nil
}
func (s *stubLaunchedWinsRepo) QueryAuditLog(_ context.Context, _ uuid.UUID, _, _ time.Time, _, _ string, _, _ int) (repository.AuditLogResult, error) {
	return repository.AuditLogResult{}, nil
}

func TestLaunchedWinsPlugin(t *testing.T) {
	flagKey := uuid.New()
	now := time.Now()
	q := analytics.Query{OrgID: uuid.New(), From: now.Add(-24 * time.Hour), To: now, Page: 1, Limit: 25}

	t.Run("returns entries with rollout percentage 100", func(t *testing.T) {
		repo := &stubLaunchedWinsRepo{result: repository.LaunchedWinsResult{
			Wins: []repository.LaunchedWin{
				{FlagKey: flagKey, FlagName: "my-flag", ActorEmail: "a@b.com", ActorName: "Alice", OccurredAt: now},
			},
		}}
		p := analytics.NewLaunchedWinsPlugin(repo)

		if p.Metric() != "launched_wins" {
			t.Fatalf("unexpected metric name: %s", p.Metric())
		}
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := out.(analytics.LaunchedWinsResponse)
		if len(resp.Wins) != 1 {
			t.Fatalf("expected 1 win, got %d", len(resp.Wins))
		}
		if resp.Wins[0].FlagKey != flagKey {
			t.Errorf("unexpected flag key: %v", resp.Wins[0].FlagKey)
		}
		if resp.Wins[0].RolloutPercentage != 100 {
			t.Errorf("expected rollout 100, got %v", resp.Wins[0].RolloutPercentage)
		}
	})

	t.Run("empty result returns empty wins slice", func(t *testing.T) {
		p := analytics.NewLaunchedWinsPlugin(&stubLaunchedWinsRepo{})
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out.(analytics.LaunchedWinsResponse).Wins) != 0 {
			t.Error("expected empty wins slice")
		}
	})

	t.Run("repo error propagates", func(t *testing.T) {
		p := analytics.NewLaunchedWinsPlugin(&stubLaunchedWinsRepo{err: errors.New("db error")})
		if _, err := p.Execute(context.Background(), q); err == nil {
			t.Error("expected error, got nil")
		}
	})
}
