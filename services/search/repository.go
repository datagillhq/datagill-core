package main

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// OrgRepository handles org row lifecycle.
// Org rows are created lazily on first integration — there is no explicit
// org registration endpoint.
type OrgRepository interface {
	CreateOrg(ctx context.Context, orgID uuid.UUID, externalOrgID string) error
}

// IntegrationsRepository is the data access interface for integration CRUD.
// repository.PostgresRepository satisfies this interface.
type IntegrationsRepository interface {
	CreateIntegration(ctx context.Context, i models.Integration) (models.Integration, error)
	GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
	ListIntegrations(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error)
	UpdateIntegration(ctx context.Context, orgID, key uuid.UUID, patch models.IntegrationPatch) (models.Integration, error)
	SoftDeleteIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
}
