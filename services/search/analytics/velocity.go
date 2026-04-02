package analytics

import "context"

// VelocityEntry is the velocity metric response.
type VelocityEntry struct {
	AverageTimeToFullRolloutMinutes float64 `json:"averageTimeToFullRolloutMinutes"`
	SampleSize                      int     `json:"sampleSize"`
}

type velocityPlugin struct{ repo AnalyticsRepository }

// NewVelocityPlugin returns a plugin for the "velocity" metric.
func NewVelocityPlugin(repo AnalyticsRepository) Plugin {
	return &velocityPlugin{repo: repo}
}

func (p *velocityPlugin) Metric() string { return "velocity" }

func (p *velocityPlugin) Execute(ctx context.Context, q Query) (any, error) {
	res, err := p.repo.QueryVelocity(ctx, q.OrgID, q.From, q.To)
	if err != nil {
		return nil, err
	}
	return VelocityEntry{
		AverageTimeToFullRolloutMinutes: res.AverageMinutes,
		SampleSize:                      res.SampleSize,
	}, nil
}
