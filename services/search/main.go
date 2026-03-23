package main

import (
	"os"

	"github.com/rs/zerolog"
)

const serviceName = "search"

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	logger := zerolog.New(os.Stdout).With().
		Timestamp().
		Str("service", serviceName).
		Logger()

	cfg := loadConfig()

	if cfg.DatabaseURL == "" {
		logger.Fatal().Msg("APP_DATABASE_URL is required")
	}

	// TODO(Phase 2B): construct PostgresSearchRepository from cfg.DatabaseURL,
	// pass it to newApp, then start the HTTP server and handle graceful shutdown.
	logger.Fatal().Msg("PostgresSearchRepository not yet implemented — complete Phase 2B")
}
