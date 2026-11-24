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
	"github.com/datagill/datagill-core/services/search/analytics"
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
	if cfg.TemporalAddress == "" {
		logger.Fatal().Msg("TEMPORAL_ADDRESS is required")
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

	integrationsRepo, err := repository.NewPostgresRepository(ctx, cfg.DatabaseURL, cfg.EncryptionKey)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect integrations repository")
	}
	defer integrationsRepo.Close()

	analyticsRegistry := analytics.NewRegistry()
	if err := analytics.RegisterAll(analyticsRegistry, integrationsRepo); err != nil {
		logger.Fatal().Err(err).Msg("failed to register analytics plugins")
	}

	tc, err := newTemporalClient(cfg.TemporalAddress)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to dial Temporal")
	}
	defer tc.Close()

	srv := newHTTPServer(":"+cfg.Port, newApp(cfg, logger, integrationsRepo, integrationsRepo, newValidatorRegistry(), newTemporalScheduler(tc), jwtKeyfunc, analyticsRegistry).router())

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

// Server timeouts, per docs/engineering-standards.md. Without ReadTimeout and IdleTimeout a
// client can hold a connection open indefinitely by sending a body slowly or by idling.
const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 30 * time.Second
	// serverWriteTimeout is a little longer than the router's 30s middleware.Timeout, so a
	// handler that runs out of time can still write its 504 before the connection closes.
	serverWriteTimeout = 35 * time.Second
	serverIdleTimeout  = 120 * time.Second
)

// newHTTPServer returns the search service's HTTP server with every timeout set.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
}
