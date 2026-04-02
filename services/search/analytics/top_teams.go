package analytics

import "context"

// TopTeamEntry is a single item in the top_teams metric response.
type TopTeamEntry struct {
	TeamName    string `json:"teamName"`
	ChangeCount int    `json:"changeCount"`
}

type topTeamsPlugin struct{ repo AnalyticsRepository }

// NewTopTeamsPlugin returns a plugin for the "top_teams" metric.
func NewTopTeamsPlugin(repo AnalyticsRepository) Plugin {
	return &topTeamsPlugin{repo: repo}
}

func (p *topTeamsPlugin) Metric() string { return "top_teams" }

func (p *topTeamsPlugin) Execute(ctx context.Context, q Query) (any, error) {
	res, err := p.repo.QueryTopTeams(ctx, q.OrgID, q.From, q.To)
	if err != nil {
		return nil, err
	}
	entries := make([]TopTeamEntry, 0, len(res.Teams))
	for _, t := range res.Teams {
		entries = append(entries, TopTeamEntry{TeamName: t.TeamName, ChangeCount: t.ChangeCount})
	}
	return entries, nil
}
