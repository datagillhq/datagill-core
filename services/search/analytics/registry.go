package analytics

import "fmt"

// ErrUnknownMetric is returned when a metric name has no registered plugin.
var ErrUnknownMetric = fmt.Errorf("unknown metric")

// Registry holds the set of registered metric plugins.
type Registry struct {
	plugins map[string]Plugin
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{plugins: make(map[string]Plugin)}
}

// Register adds a plugin to the registry. Panics on duplicate metric names.
func (r *Registry) Register(p Plugin) {
	if _, exists := r.plugins[p.Metric()]; exists {
		panic(fmt.Sprintf("analytics: duplicate metric %q", p.Metric()))
	}
	r.plugins[p.Metric()] = p
}

// Get returns the plugin for the given metric name, or ErrUnknownMetric.
func (r *Registry) Get(metric string) (Plugin, error) {
	p, ok := r.plugins[metric]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownMetric, metric)
	}
	return p, nil
}
