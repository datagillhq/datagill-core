package analytics

// RegisterAll registers every built-in metric plugin into r.
// Call this once at startup before serving requests.
func RegisterAll(r *Registry, repo AnalyticsRepository) error {
	plugins := []Plugin{
		NewLaunchedWinsPlugin(repo),
		NewTopTeamsPlugin(repo),
		NewVelocityPlugin(repo),
		NewAuditLogPlugin(repo),
	}
	for _, p := range plugins {
		if err := r.Register(p); err != nil {
			return err
		}
	}
	return nil
}
