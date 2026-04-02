package analytics

import "fmt"

// ErrUnknownMetric is returned when a metric name has no registered plugin.
var ErrUnknownMetric = fmt.Errorf("unknown metric")

// ErrDuplicateMetric is returned when registering a metric name that is already registered.
var ErrDuplicateMetric = fmt.Errorf("duplicate metric")

// Registry holds the set of registered metric plugins.
type Registry struct {
	plugins map[string]Plugin
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{plugins: make(map[string]Plugin)}
}

// Register adds a plugin to the registry. Returns ErrDuplicateMetric if the
// metric name is already registered.
func (r *Registry) Register(p Plugin) error {
	if _, exists := r.plugins[p.Metric()]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateMetric, p.Metric())
	}
	r.plugins[p.Metric()] = p
	return nil
}

// Get returns the plugin for the given metric name, or ErrUnknownMetric.
func (r *Registry) Get(metric string) (Plugin, error) {
	p, ok := r.plugins[metric]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownMetric, metric)
	}
	return p, nil
}
