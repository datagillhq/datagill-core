package models

import (
	"time"

	"github.com/google/uuid"
)

// FlagState is the operational state of a feature flag (off / on / partial).
type FlagState string

const (
	FlagStateOff     FlagState = "off"
	FlagStateOn      FlagState = "on"
	FlagStatePartial FlagState = "partial"
)

// EventType describes what kind of change occurred in a changelog entry.
type EventType string

const (
	EventTypeCreated  EventType = "created"
	EventTypeUpdated  EventType = "updated"
	EventTypeToggled  EventType = "toggled"
	EventTypeDeleted  EventType = "deleted"
	EventTypeRollback EventType = "rollback"
)

// LifecyclePurpose classifies whether a flag is expected to be temporary or permanent.
type LifecyclePurpose string

const (
	LifecyclePurposeTemporary LifecyclePurpose = "temporary"
	LifecyclePurposePermanent LifecyclePurpose = "permanent"
)

// LifecycleState reflects the health/activity state of a flag over time.
type LifecycleState string

const (
	LifecycleStateActive     LifecycleState = "active"
	LifecycleStateStale      LifecycleState = "stale"
	LifecycleStateDead       LifecycleState = "dead"
	LifecycleStateArchived   LifecycleState = "archived"
	LifecycleStateDeprecated LifecycleState = "deprecated"
)

// FeatureFlagData is the normalized payload stored in feature_flags.data JSONB.
// Fields unavailable from a given provider are omitted (zero value).
type FeatureFlagData struct {
	Name         string                     `json:"name"`
	Description  string                     `json:"description,omitempty"`
	Source       FlagSource                 `json:"source"`
	Maintainer   *FlagMaintainer            `json:"maintainer,omitempty"`
	Lifecycle    FlagLifecycle              `json:"lifecycle"`
	Environments map[string]FlagEnvironment `json:"environments"`
}

// FlagSource holds provider-origin metadata for a flag.
type FlagSource struct {
	Key       string      `json:"key"`
	Provider  Source      `json:"provider"`
	Project   FlagProject `json:"project"`
	CreatedAt *time.Time  `json:"created_at,omitempty"`
}

// FlagProject identifies the provider project a flag belongs to.
type FlagProject struct {
	ID string `json:"id"`
}

// FlagMaintainer holds optional ownership information for a flag.
type FlagMaintainer struct {
	Person *FlagMaintainerPerson `json:"person,omitempty"`
	Team   *FlagMaintainerTeam   `json:"team,omitempty"`
}

// FlagMaintainerPerson holds the email of the individual who owns the flag.
type FlagMaintainerPerson struct {
	Email string `json:"email"`
}

// FlagMaintainerTeam holds the name of the team that owns the flag.
type FlagMaintainerTeam struct {
	Name string `json:"name"`
}

// FlagLifecycle captures the purpose and health state of a flag.
type FlagLifecycle struct {
	Purpose LifecyclePurpose `json:"purpose"`
	State   LifecycleState   `json:"state"`
}

// FlagEnvironment holds the per-environment enabled state of a flag.
type FlagEnvironment struct {
	Enabled           bool     `json:"enabled"`
	RolloutPercentage *float64 `json:"rollout_percentage,omitempty"`
}

// FeatureFlag maps to the feature_flags table.
type FeatureFlag struct {
	ID              int64
	Key             uuid.UUID
	SourceRecordKey uuid.UUID
	IntegrationID   uuid.UUID
	OrgID           uuid.UUID
	State           FlagState
	Data            FeatureFlagData
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ChangelogData is the normalized payload stored in feature_flag_changelog.data JSONB.
type ChangelogData struct {
	Description  string         `json:"description,omitempty"`
	Actor        ChangelogActor `json:"actor"`
	Source       FlagSource     `json:"source"`
	Environments []string       `json:"environments"`
	ChangeType   string         `json:"change_type"`
}

// ChangelogActor identifies who made a change.
type ChangelogActor struct {
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
}

// RecordState is the soft-delete state of a database record.
type RecordState string

const (
	RecordStateActive  RecordState = "active"
	RecordStateDeleted RecordState = "deleted"
)

// FeatureFlagChangelog maps to the feature_flag_changelog table.
type FeatureFlagChangelog struct {
	ID              int64
	Key             uuid.UUID
	SourceRecordKey uuid.UUID
	FeatureFlagKey  uuid.UUID
	IntegrationID   uuid.UUID
	OrgID           uuid.UUID
	EventType       EventType
	State           RecordState
	OccurredAt      time.Time
	Data            ChangelogData
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
