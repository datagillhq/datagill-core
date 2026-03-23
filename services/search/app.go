package main

import "github.com/rs/zerolog"

type app struct {
	config     config
	logger     zerolog.Logger
	repository SearchRepository
}

func newApp(cfg config, logger zerolog.Logger, repo SearchRepository) *app {
	return &app{
		config:     cfg,
		logger:     logger,
		repository: repo,
	}
}
