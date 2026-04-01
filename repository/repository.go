// Package repository defines the persistence interface and its PostgreSQL
// implementation. All methods are idempotent — callers may safely retry any
// operation without risk of duplicate data.
package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
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

	// GetIntegration returns a single non-deleted integration by key, scoped to the org.
	GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)

	// ListIntegrations returns all active and paused integrations for an org.
	ListIntegrations(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error)

	// CreateIntegration inserts a new integration and returns it with DB-assigned fields.
	CreateIntegration(ctx context.Context, i models.Integration) (models.Integration, error)

	// UpdateIntegration applies a partial update scoped to the org. Only non-nil
	// patch fields are written; the rest are left unchanged.
	UpdateIntegration(ctx context.Context, orgID, key uuid.UUID, patch models.IntegrationPatch) (models.Integration, error)

	// SoftDeleteIntegration sets state = deleted on the integration, scoped to the org.
	SoftDeleteIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)

	// CreateOrg inserts an org row if one does not already exist. Safe to call
	// multiple times — uses INSERT ... ON CONFLICT DO NOTHING.
	CreateOrg(ctx context.Context, orgID uuid.UUID, externalOrgID string) error
}
