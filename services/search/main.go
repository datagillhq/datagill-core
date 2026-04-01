package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/repository"
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
	if cfg.AuthURL == "" {
		logger.Fatal().Msg("AUTH_URL is required")
	}
	// EncryptionKey is nil when ENCRYPTION_KEY is unset or not valid hex;
	// either way the length check catches it with a clear error.
	if len(cfg.EncryptionKey) != 32 {
		logger.Fatal().Msg("ENCRYPTION_KEY must be a 64-character hex string (32 bytes) — generate with: openssl rand -hex 32")
	}

	ctx := context.Background()

	jwtKeyfunc, err := fetchJWKSKeyfunc(cfg.AuthURL + "/.well-known/jwks.json")
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to fetch JWKS")
	}

	repo, err := NewPostgresSearchRepository(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer repo.Close()

	integrationsRepo, err := repository.NewPostgresRepository(ctx, cfg.DatabaseURL, cfg.EncryptionKey)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect integrations repository")
	}
	defer integrationsRepo.Close()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           newApp(cfg, logger, repo, integrationsRepo, integrationsRepo, newValidatorRegistry(), jwtKeyfunc).router(),
		ReadHeaderTimeout: 10 * time.Second, // TODO: tune timeout values based on observed p99 latency
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info().Str("port", cfg.Port).Msg("server started")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal().Err(err).Msg("server error")
		}
	}()

	<-quit
	logger.Info().Msg("shutting down")

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("shutdown error")
	}
}
