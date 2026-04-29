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
	SourceGitHub       Source = "github"
)

// IntegrationState is the operational state of an integration row.
type IntegrationState string

const (
	IntegrationStateActive  IntegrationState = "active"
	IntegrationStatePaused  IntegrationState = "paused"
	IntegrationStateDeleted IntegrationState = "deleted"
)

// IntegrationConfig holds provider-specific configuration stored in integrations.config JSONB.
// Poll interval and backfill window are owned internally by DataGill and are not
// client-configurable. Project context lives at the feature flag level, not here.
type IntegrationConfig struct {
	EnvironmentKey      string `json:"environment_key,omitempty"`
	InstallationID      int64  `json:"installation_id,omitempty"`
	AccountLogin        string `json:"account_login,omitempty"`
	AccountType         string `json:"account_type,omitempty"`
	RepositorySelection string `json:"repository_selection,omitempty"`
}

// IntegrationPatch carries optional fields for a partial integration update.
// Only non-nil fields are applied; nil means "leave unchanged".
type IntegrationPatch struct {
	APIKey *string
	State  *IntegrationState
}

// Integration maps to the integrations table.
type Integration struct {
	ID     int64
	Key    uuid.UUID
	OrgID  uuid.UUID
	Source Source
	APIKey string // AES-256-GCM encrypted at rest
	Config IntegrationConfig
	State  IntegrationState
	// LastSyncedAt is temporary LaunchDarkly compatibility state.
	// New providers must keep their sync cursor in Temporal workflow state instead.
	LastSyncedAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
