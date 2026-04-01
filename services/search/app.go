package main

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
)

type app struct {
	config           config
	logger           zerolog.Logger
	searchRepo       SearchRepository
	integrationsRepo IntegrationsRepository
	orgRepo          OrgRepository
	validator        IntegrationValidator
	jwtKeyfunc       jwt.Keyfunc
}

func newApp(cfg config, logger zerolog.Logger, searchRepo SearchRepository, integrationsRepo IntegrationsRepository, orgRepo OrgRepository, validator IntegrationValidator, jwtKeyfunc jwt.Keyfunc) *app {
	return &app{
		config:           cfg,
		logger:           logger,
		searchRepo:       searchRepo,
		integrationsRepo: integrationsRepo,
		orgRepo:          orgRepo,
		validator:        validator,
		jwtKeyfunc:       jwtKeyfunc,
	}
}
