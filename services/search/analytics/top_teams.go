package analytics

import "context"

// TopTeamEntry is a single team ranked by flag change activity.
type TopTeamEntry struct {
	TeamName    string `json:"teamName"`
	ChangeCount int    `json:"changeCount"`
}

// TopTeamsResponse is the top_teams plugin response envelope.
type TopTeamsResponse struct {
	Teams []TopTeamEntry `json:"teams"`
}

type topTeamsPlugin struct{ repo AnalyticsRepository }

// NewTopTeamsPlugin returns a plugin for the "top_teams" metric.
func NewTopTeamsPlugin(repo AnalyticsRepository) Plugin {
	return &topTeamsPlugin{repo: repo}
}

func (p *topTeamsPlugin) Metric() string    { return "top_teams" }
func (p *topTeamsPlugin) ResponseType() any { return TopTeamsResponse{} }

func (p *topTeamsPlugin) Execute(ctx context.Context, q Query) (any, error) {
	res, err := p.repo.QueryTopTeams(ctx, q.OrgID, q.From, q.To)
	if err != nil {
		return nil, err
	}
	entries := make([]TopTeamEntry, 0, len(res.Teams))
	for _, t := range res.Teams {
		entries = append(entries, TopTeamEntry{TeamName: t.TeamName, ChangeCount: t.ChangeCount})
	}
	return TopTeamsResponse{Teams: entries}, nil
}
