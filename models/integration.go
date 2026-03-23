package models

import (
	"time"

	"github.com/google/uuid"
)

// Source identifies a feature flag provider.
type Source string

const (
	SourceLaunchDarkly Source = "launchdarkly"
	SourceUnleash      Source = "unleash"
	SourceFlagsmith    Source = "flagsmith"
)

// IntegrationState is the operational state of an integration row.
type IntegrationState string

const (
	IntegrationStateActive  IntegrationState = "active"
	IntegrationStatePaused  IntegrationState = "paused"
	IntegrationStateDeleted IntegrationState = "deleted"
)

// IntegrationConfig holds provider-specific configuration stored in integrations.config JSONB.
type IntegrationConfig struct {
	ProjectKey          string `json:"project_key"`
	EnvironmentKey      string `json:"environment_key"`
	PollIntervalMinutes int    `json:"poll_interval_minutes"`
	BackfillDays        int    `json:"backfill_days"`
}

// Integration maps to the integrations table.
type Integration struct {
	ID        int64
	Key       uuid.UUID
	OrgID     uuid.UUID
	Source    Source
	APIKey    string // AES-256-GCM encrypted at rest
	Config    IntegrationConfig
	State     IntegrationState
	CreatedAt time.Time
	UpdatedAt time.Time
}
