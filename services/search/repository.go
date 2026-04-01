package main

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// IntegrationsRepository is the data access interface for integration CRUD.
// repository.PostgresRepository satisfies this interface.
type IntegrationsRepository interface {
	CreateOrg(ctx context.Context, orgID uuid.UUID, externalOrgID string) error
	CreateIntegration(ctx context.Context, i models.Integration) (models.Integration, error)
	GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
	ListIntegrations(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error)
	UpdateIntegration(ctx context.Context, orgID, key uuid.UUID, patch models.IntegrationPatch) (models.Integration, error)
	SoftDeleteIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
}

// SearchRepository is the data access interface for the search service.
// The concrete implementation (PostgresSearchRepository) is wired up in
// Phase 2B.
type SearchRepository interface {
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
	ListFlags(ctx context.Context, orgID string, filters FlagFilters) ([]FlagSummary, error)
	GetFlag(ctx context.Context, orgID, flagKey string) (FlagDetail, error)
	GetFlagHistory(ctx context.Context, orgID, flagKey string, p Pagination) ([]ChangelogEntry, error)
	GetTimeline(ctx context.Context, orgID string, p Pagination) ([]ChangelogEntry, error)
}
