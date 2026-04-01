package main

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
)

type app struct {
	config     config
	logger     zerolog.Logger
	repository SearchRepository
	jwtKeyfunc jwt.Keyfunc
}

func newApp(cfg config, logger zerolog.Logger, repo SearchRepository, jwtKeyfunc jwt.Keyfunc) *app {
	return &app{
		config:     cfg,
		logger:     logger,
		repository: repo,
		jwtKeyfunc: jwtKeyfunc,
	}
}
