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

func TestTopTeamsPlugin(t *testing.T) {
	q := analytics.Query{OrgID: uuid.New(), From: time.Now().Add(-24 * time.Hour), To: time.Now()}

	t.Run("returns teams ordered by change count", func(t *testing.T) {
		repo := &fakeAnalyticsRepo{topTeams: repository.TopTeamsResult{
			Teams: []repository.TopTeam{
				{TeamName: "alpha", ChangeCount: 5},
				{TeamName: "beta", ChangeCount: 2},
			},
		}}
		p := analytics.NewTopTeamsPlugin(repo)

		if p.Metric() != "top_teams" {
			t.Fatalf("unexpected metric name: %s", p.Metric())
		}
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := out.(analytics.TopTeamsResponse)
		if len(resp.Teams) != 2 {
			t.Fatalf("expected 2 teams, got %d", len(resp.Teams))
		}
		if resp.Teams[0].TeamName != "alpha" || resp.Teams[0].ChangeCount != 5 {
			t.Errorf("unexpected first team: %+v", resp.Teams[0])
		}
	})

	t.Run("empty result returns empty teams slice", func(t *testing.T) {
		p := analytics.NewTopTeamsPlugin(&fakeAnalyticsRepo{})
		out, err := p.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out.(analytics.TopTeamsResponse).Teams) != 0 {
			t.Error("expected empty teams slice")
		}
	})

	t.Run("repo error propagates", func(t *testing.T) {
		p := analytics.NewTopTeamsPlugin(&fakeAnalyticsRepo{err: errors.New("db error")})
		if _, err := p.Execute(context.Background(), q); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("ResponseType returns TopTeamsResponse", func(t *testing.T) {
		p := analytics.NewTopTeamsPlugin(&fakeAnalyticsRepo{})
		if _, ok := p.ResponseType().(analytics.TopTeamsResponse); !ok {
			t.Errorf("expected TopTeamsResponse, got %T", p.ResponseType())
		}
	})
}
