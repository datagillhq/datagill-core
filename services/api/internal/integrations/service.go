// Package integrations holds the rules for connecting, changing and removing an org's
// provider integrations. It is the only place that creates integrations and their
// schedules; the HTTP layer and the GitHub install flow both go through it.
package integrations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
)

// Errors returned by Service. Callers map them to user-facing responses.
var (
	ErrNotFound            = errors.New("integration not found")
	ErrDuplicateProvider   = errors.New("org already has an integration for this provider")
	ErrUseGitHubApp        = errors.New("github integrations are connected through the GitHub App install flow")
	ErrInvalidAPIKey       = errors.New("provider rejected the API key")
	ErrProviderUnreachable = errors.New("provider unreachable")
	ErrInvalidState        = errors.New("state must be active or paused")
)

// rollbackTimeout bounds the cleanup after a failed create. It runs on a context detached
// from the request, since the request may be the thing that timed out.
const rollbackTimeout = 5 * time.Second

// Repository is the integration storage the service needs.
type Repository interface {
	CreateIntegration(ctx context.Context, i models.Integration) (models.Integration, error)
	GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
	ListIntegrations(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error)
	UpsertGitHubIntegration(ctx context.Context, orgID uuid.UUID, config models.IntegrationConfig) (models.Integration, bool, error)
	UpdateIntegration(ctx context.Context, orgID, key uuid.UUID, patch models.IntegrationPatch) (models.Integration, error)
	SoftDeleteIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
}

// OrgRepository creates org rows. Orgs are created lazily on their first integration;
// there is no org registration endpoint.
type OrgRepository interface {
	CreateOrg(ctx context.Context, orgID uuid.UUID, externalOrgID string) error
}

// Scheduler ensures the sync schedules for an integration exist. It must be idempotent.
type Scheduler interface {
	Create(ctx context.Context, integration models.Integration) error
}

// KeyValidator checks an API key against one provider. It returns ErrInvalidAPIKey when
// the provider rejects the key and ErrProviderUnreachable when it can't tell.
type KeyValidator interface {
	Validate(ctx context.Context, apiKey string) error
}

// Service manages an org's integrations.
type Service struct {
	repo       Repository
	orgs       OrgRepository
	scheduler  Scheduler
	validators map[models.Source]KeyValidator
}

// NewService returns a Service. validators lists the API-key providers that can be
// connected through Create, keyed by source.
func NewService(repo Repository, orgs OrgRepository, scheduler Scheduler, validators map[models.Source]KeyValidator) *Service {
	return &Service{repo: repo, orgs: orgs, scheduler: scheduler, validators: validators}
}

// Providers lists every provider an org can connect: the API-key providers, then GitHub.
func (s *Service) Providers() []models.Source {
	providers := make([]models.Source, 0, len(s.validators)+1)
	for source := range s.validators {
		providers = append(providers, source)
	}
	slices.Sort(providers)
	return append(providers, models.SourceGitHub)
}

// CreateInput is a request to connect an API-key provider.
type CreateInput struct {
	Source models.Source
	APIKey string
	Config models.IntegrationConfig
}

// Create connects an API-key provider for the org and schedules its first sync.
// An org may have one integration per provider.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, in CreateInput) (models.Integration, error) {
	if in.Source == models.SourceGitHub {
		return models.Integration{}, ErrUseGitHubApp
	}

	existing, err := s.repo.ListIntegrations(ctx, orgID)
	if err != nil {
		return models.Integration{}, fmt.Errorf("list integrations: %w", err)
	}
	if slices.ContainsFunc(existing, func(i models.Integration) bool { return i.Source == in.Source }) {
		return models.Integration{}, ErrDuplicateProvider
	}

	if err := s.validateKey(ctx, in.Source, in.APIKey); err != nil {
		return models.Integration{}, err
	}

	if err := s.orgs.CreateOrg(ctx, orgID, orgID.String()); err != nil {
		return models.Integration{}, fmt.Errorf("create org: %w", err)
	}
	created, err := s.repo.CreateIntegration(ctx, models.Integration{
		OrgID:  orgID,
		Source: in.Source,
		APIKey: in.APIKey,
		Config: in.Config,
	})
	if err != nil {
		return models.Integration{}, fmt.Errorf("create integration: %w", err)
	}
	if err := s.scheduler.Create(ctx, created); err != nil {
		err = fmt.Errorf("create schedules for %s: %w", created.Key, err)
		return models.Integration{}, errors.Join(err, s.rollback(ctx, created))
	}
	return created, nil
}

// rollback soft-deletes an integration whose schedules could not be created. Without them
// it would never sync, and the duplicate-provider check would reject every retry with 409,
// leaving the user stuck until they found and deleted it.
func (s *Service) rollback(ctx context.Context, created models.Integration) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	if _, err := s.repo.SoftDeleteIntegration(ctx, created.OrgID, created.Key); err != nil {
		return fmt.Errorf("roll back integration %s after schedule failure: %w", created.Key, err)
	}
	return nil
}

// ConnectGitHub records a GitHub App installation for the org and ensures its schedules
// exist. It runs on every install callback: reconnecting the same installation updates
// the row, and a connect that failed after saving the row recovers here.
func (s *Service) ConnectGitHub(ctx context.Context, orgID uuid.UUID, config models.IntegrationConfig) (models.Integration, error) {
	if err := s.orgs.CreateOrg(ctx, orgID, orgID.String()); err != nil {
		return models.Integration{}, fmt.Errorf("create org: %w", err)
	}
	integration, _, err := s.repo.UpsertGitHubIntegration(ctx, orgID, config)
	if err != nil {
		return models.Integration{}, fmt.Errorf("upsert github integration: %w", err)
	}
	if err := s.scheduler.Create(ctx, integration); err != nil {
		return models.Integration{}, fmt.Errorf("create schedules for %s: %w", integration.Key, err)
	}
	return integration, nil
}

// List returns the org's active and paused integrations.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error) {
	integrations, err := s.repo.ListIntegrations(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	return integrations, nil
}

// Update applies a partial update: pause, resume, or a new API key, which is validated
// against the provider first.
func (s *Service) Update(ctx context.Context, orgID, key uuid.UUID, patch models.IntegrationPatch) (models.Integration, error) {
	// "deleted" has its own endpoint, and any other value would hide the integration from
	// the list while leaving it otherwise live.
	if patch.State != nil && *patch.State != models.IntegrationStateActive && *patch.State != models.IntegrationStatePaused {
		return models.Integration{}, ErrInvalidState
	}
	if patch.APIKey != nil {
		existing, err := s.repo.GetIntegration(ctx, orgID, key)
		if err != nil {
			return models.Integration{}, notFoundOr(err, "get integration")
		}
		if err := s.validateKey(ctx, existing.Source, *patch.APIKey); err != nil {
			return models.Integration{}, err
		}
	}

	updated, err := s.repo.UpdateIntegration(ctx, orgID, key, patch)
	if err != nil {
		return models.Integration{}, notFoundOr(err, "update integration")
	}
	return updated, nil
}

// Delete soft-deletes an integration. Its schedules stay; each run checks the state.
func (s *Service) Delete(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error) {
	deleted, err := s.repo.SoftDeleteIntegration(ctx, orgID, key)
	if err != nil {
		return models.Integration{}, notFoundOr(err, "delete integration")
	}
	return deleted, nil
}

func (s *Service) validateKey(ctx context.Context, source models.Source, apiKey string) error {
	v, ok := s.validators[source]
	if !ok {
		return fmt.Errorf("%w: no API-key provider %q", ErrProviderUnreachable, source)
	}
	return v.Validate(ctx, apiKey)
}

// notFoundOr maps the repository's not-found error to ErrNotFound and wraps anything else.
// Another org's integration is also "not found", so callers can't probe for keys.
func notFoundOr(err error, op string) error {
	if errors.Is(err, repository.ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}
