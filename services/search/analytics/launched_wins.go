package analytics

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// LaunchedWinEntry is a single item in the launched_wins metric response.
type LaunchedWinEntry struct {
	FlagKey           uuid.UUID `json:"flagKey"`
	FlagName          string    `json:"flagName"`
	ActorEmail        string    `json:"actorEmail"`
	ActorName         string    `json:"actorName"`
	RolloutPercentage float64   `json:"rolloutPercentage"`
	OccurredAt        time.Time `json:"occurredAt"`
}

type launchedWinsPlugin struct{ repo AnalyticsRepository }

// NewLaunchedWinsPlugin returns a plugin for the "launched_wins" metric.
func NewLaunchedWinsPlugin(repo AnalyticsRepository) Plugin {
	return &launchedWinsPlugin{repo: repo}
}

func (p *launchedWinsPlugin) Metric() string { return "launched_wins" }

func (p *launchedWinsPlugin) Execute(ctx context.Context, q Query) (any, error) {
	res, err := p.repo.QueryLaunchedWins(ctx, q.OrgID, q.From, q.To, q.Team, q.Actor)
	if err != nil {
		return nil, err
	}
	entries := make([]LaunchedWinEntry, 0, len(res.Wins))
	for _, w := range res.Wins {
		entries = append(entries, LaunchedWinEntry{
			FlagKey:           w.FlagKey,
			FlagName:          w.FlagName,
			ActorEmail:        w.ActorEmail,
			ActorName:         w.ActorName,
			RolloutPercentage: 100,
			OccurredAt:        w.OccurredAt,
		})
	}
	return entries, nil
}
