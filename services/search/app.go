package main

import "log/slog"

type app struct {
	config     config
	logger     *slog.Logger
	repository SearchRepository
}

func newApp(cfg config, logger *slog.Logger) *app {
	return &app{
		config:     cfg,
		logger:     logger,
		repository: newMemorySearchRepository(),
	}
}
