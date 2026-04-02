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

func TestLaunchedWinsPlugin(t *testing.T) {
	flagKey := uuid.New()
	now := time.Now()
	q := analytics.Query{OrgID: uuid.New(), From: now.Add(-24 * time.Hour), To: now, Page: 1, Limit: 25}

	t.Run("returns entries with rollout percentage 100", func(t *testing.T) {
		repo := &fakeAnalyticsRepo{launchedWins: repository.LaunchedWinsResult{
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
		p := analytics.NewLaunchedWinsPlugin(&fakeAnalyticsRepo{})
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out.(analytics.LaunchedWinsResponse).Wins) != 0 {
			t.Error("expected empty wins slice")
		}
	})

	t.Run("repo error propagates", func(t *testing.T) {
		p := analytics.NewLaunchedWinsPlugin(&fakeAnalyticsRepo{launchedWinsErr: errors.New("db error")})
		if _, err := p.Execute(context.Background(), q); err == nil {
			t.Error("expected error, got nil")
		}
	})
}

func TestLaunchedWinsPlugin_ResponseType(t *testing.T) {
	p := analytics.NewLaunchedWinsPlugin(&fakeAnalyticsRepo{})
	if _, ok := p.ResponseType().(analytics.LaunchedWinsResponse); !ok {
		t.Errorf("expected ResponseType to return LaunchedWinsResponse, got %T", p.ResponseType())
	}
}
