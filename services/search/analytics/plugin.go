package analytics

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Query holds the parameters passed to every metric plugin.
type Query struct {
	OrgID uuid.UUID
	From  time.Time
	To    time.Time
	Team  string // empty = no filter
	Actor string // empty = no filter
	Page  int    // for paginated metrics
	Limit int    // for paginated metrics
}

// Plugin is the interface every analytics metric must implement.
type Plugin interface {
	// Metric returns the metric name used to look up this plugin (e.g. "launched_wins").
	Metric() string
	// Execute runs the metric query and returns the result payload.
	Execute(ctx context.Context, q Query) (any, error)
}
