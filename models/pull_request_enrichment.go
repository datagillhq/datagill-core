package models

import (
	"time"

	"github.com/google/uuid"
)

// PREnrichmentStatus tracks the enrichment lifecycle of a pull request.
type PREnrichmentStatus string

const (
	PREnrichmentStatusPending    PREnrichmentStatus = "pending"
	PREnrichmentStatusInProgress PREnrichmentStatus = "in_progress"
	PREnrichmentStatusCompleted  PREnrichmentStatus = "completed"
	PREnrichmentStatusFailed     PREnrichmentStatus = "failed"
)

// PullRequestEnrichment maps to the pull_request_enrichments table.
type PullRequestEnrichment struct {
	PullRequestKey uuid.UUID
	OrgID          uuid.UUID
	Status         PREnrichmentStatus
	Attempts       int
	Data           *PullRequestEnrichmentData
	EnrichedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// PullRequestEnrichmentData is the computed enrichment output stored in the enrichment JSONB column.
type PullRequestEnrichmentData struct {
	FeatureFlags PullRequestFeatureFlagRefs `json:"feature_flags"`
}

// PullRequestFeatureFlagRefs holds flag UUIDs found in the PR diff, split by add/remove.
type PullRequestFeatureFlagRefs struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}
