// Package repository defines the persistence interface and its PostgreSQL
// implementation. All methods are idempotent — callers may safely retry any
// operation without risk of duplicate data.
package repository

import (
	"context"

	"github.com/datagill/datagill-core/models"
	"github.com/google/uuid"
)

// Repository is the persistence interface used by ETL activities and the
// Search Service. Defined here (in the consuming package) so implementations
// can be swapped without changing call sites.
type Repository interface {
	// SaveSourceRecord persists a raw provider API response.
	// Upserts on source_records.key — safe to call multiple times with the same record.
	SaveSourceRecord(ctx context.Context, r models.SourceRecord) error

	// SaveFeatureFlag upserts the normalized state of a feature flag.
	// Upserts on feature_flags.key.
	SaveFeatureFlag(ctx context.Context, f models.FeatureFlag) error

	// SaveChangelog upserts a normalized changelog entry.
	// Upserts on feature_flag_changelog.key.
	SaveChangelog(ctx context.Context, c models.FeatureFlagChangelog) error

	// GetIntegration returns a single integration by its key.
	GetIntegration(ctx context.Context, integrationKey uuid.UUID) (models.Integration, error)

	// ListIntegrations returns all active integrations for an org.
	ListIntegrations(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error)
}
