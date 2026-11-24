package analytics

import "context"

// VelocityResponse is the velocity plugin response envelope.
// SampleSize is always returned so the frontend can show a disclaimer
// when it is below a meaningful threshold (e.g. < 3).
type VelocityResponse struct {
	AverageTimeToFullRolloutMinutes float64 `json:"averageTimeToFullRolloutMinutes"`
	SampleSize                      int     `json:"sampleSize"`
}

type velocityPlugin struct{ repo AnalyticsRepository }

// NewVelocityPlugin returns a plugin for the "velocity" metric.
func NewVelocityPlugin(repo AnalyticsRepository) Plugin {
	return &velocityPlugin{repo: repo}
}

func (p *velocityPlugin) Metric() string    { return "velocity" }
func (p *velocityPlugin) ResponseType() any { return VelocityResponse{} }

func (p *velocityPlugin) Execute(ctx context.Context, q Query) (any, error) {
	res, err := p.repo.QueryVelocity(ctx, q.OrgID, q.From, q.To)
	if err != nil {
		return nil, err
	}
	return VelocityResponse{
		AverageTimeToFullRolloutMinutes: res.AverageMinutes,
		SampleSize:                      res.SampleSize,
	}, nil
}
