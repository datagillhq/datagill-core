package main

import (
	"context"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/services/search/analytics"
)

type GitHubAuthService interface {
	Start(ctx context.Context, orgID uuid.UUID, userID, returnTo string) (githubStartResponse, error)
}

type app struct {
	config            config
	logger            zerolog.Logger
	integrationsRepo  IntegrationsRepository
	orgRepo           OrgRepository
	validator         IntegrationValidator
	scheduler         Scheduler
	jwtKeyfunc        jwt.Keyfunc
	analyticsRegistry *analytics.Registry
	githubAuth        GitHubAuthService
}

func newApp(cfg config, logger zerolog.Logger, integrationsRepo IntegrationsRepository, orgRepo OrgRepository, validator IntegrationValidator, scheduler Scheduler, jwtKeyfunc jwt.Keyfunc, analyticsRegistry *analytics.Registry) *app {
	return newAppWithGitHubAuth(cfg, logger, integrationsRepo, orgRepo, validator, scheduler, jwtKeyfunc, analyticsRegistry, nil)
}

func newAppWithGitHubAuth(cfg config, logger zerolog.Logger, integrationsRepo IntegrationsRepository, orgRepo OrgRepository, validator IntegrationValidator, scheduler Scheduler, jwtKeyfunc jwt.Keyfunc, analyticsRegistry *analytics.Registry, githubAuth GitHubAuthService) *app {
	if githubAuth == nil {
		githubAuth = newGitHubAuthService(cfg)
	}
	return &app{
		config:            cfg,
		logger:            logger,
		integrationsRepo:  integrationsRepo,
		orgRepo:           orgRepo,
		validator:         validator,
		scheduler:         scheduler,
		jwtKeyfunc:        jwtKeyfunc,
		analyticsRegistry: analyticsRegistry,
		githubAuth:        githubAuth,
	}
}
