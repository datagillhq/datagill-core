// Command api is DataGill's HTTP API: auth, integrations, schedules and analytics for the
// UI. It holds no sync logic; that runs in the worker.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
	"github.com/datagill/datagill-core/services/api/analytics"
	"github.com/datagill/datagill-core/services/api/internal/config"
	"github.com/datagill/datagill-core/services/api/internal/githubapp"
	"github.com/datagill/datagill-core/services/api/internal/httpapi"
	"github.com/datagill/datagill-core/services/api/internal/integrations"
	"github.com/datagill/datagill-core/services/api/internal/jwks"
	"github.com/datagill/datagill-core/services/api/internal/schedules"
)

const (
	outboundTimeout = 10 * time.Second
	shutdownTimeout = 10 * time.Second
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	logger := zerolog.New(os.Stdout).With().Timestamp().Str("service", httpapi.ServiceName).Logger()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Fatal().Err(err).Msg("api service stopped")
	}
}

func run(ctx context.Context, logger zerolog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	outbound := &http.Client{Timeout: outboundTimeout}

	keyfunc, err := jwks.FetchKeyfunc(ctx, outbound, cfg.AuthURL+"/.well-known/jwks.json")
	if err != nil {
		return err
	}

	repo, err := repository.NewPostgresRepository(ctx, cfg.DatabaseURL, cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer repo.Close()

	temporal, err := schedules.Dial(cfg.Temporal)
	if err != nil {
		return err
	}
	defer temporal.Close()

	integrationService := integrations.NewService(repo, repo, schedules.NewTemporal(temporal), map[models.Source]integrations.KeyValidator{
		models.SourceLaunchDarkly: integrations.NewLaunchDarklyValidator(outbound),
	})

	githubFlow, err := githubapp.New(cfg.GitHubApp, integrationService, outbound)
	if err != nil {
		return err
	}

	metricPlugins, err := analyticsRegistry(repo)
	if err != nil {
		return err
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	handler, err := httpapi.New(httpapi.Deps{
		Logger:        logger,
		AllowedOrigin: cfg.AllowedOrigin,
		FrontendURL:   cfg.FrontendURL,
		Keyfunc:       keyfunc,
		Integrations:  integrationService,
		GitHub:        githubFlow,
		Analytics:     metricPlugins,
		Metrics:       registry,
	})
	if err != nil {
		return err
	}

	return serve(ctx, logger, newHTTPServer(":"+cfg.Port, handler))
}

// Server timeouts, per docs/engineering-standards.md. Without ReadTimeout and IdleTimeout a
// client can hold a connection open indefinitely by sending a body slowly or by idling.
const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 30 * time.Second
	// serverWriteTimeout is a little longer than the router's 30s request timeout, so a
	// handler that runs out of time can still write its 504 before the connection closes.
	serverWriteTimeout = 35 * time.Second
	serverIdleTimeout  = 120 * time.Second
)

// newHTTPServer returns the API service's HTTP server with every timeout set.
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

func analyticsRegistry(repo analytics.AnalyticsRepository) (*analytics.Registry, error) {
	reg := analytics.NewRegistry()
	if err := analytics.RegisterAll(reg, repo); err != nil {
		return nil, fmt.Errorf("register analytics plugins: %w", err)
	}
	return reg, nil
}

// serve runs srv until ctx is canceled, then drains in-flight requests.
func serve(ctx context.Context, logger zerolog.Logger, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", srv.Addr).Msg("server started")
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
	}

	logger.Info().Msg("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}
