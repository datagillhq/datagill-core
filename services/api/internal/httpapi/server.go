// Package httpapi is the API service's HTTP layer: routing, middleware, and handlers that
// decode requests, call a service, and map the result to a response. Business rules live
// in the services it calls, not here.
package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/services/api/analytics"
	"github.com/datagill/datagill-core/services/api/internal/githubapp"
	"github.com/datagill/datagill-core/services/api/internal/integrations"
)

// ServiceName identifies this service in health checks, logs and metric names.
const ServiceName = "api"

const requestTimeout = 30 * time.Second

// Integrations manages an org's integrations.
type Integrations interface {
	Providers() []models.Source
	Create(ctx context.Context, orgID uuid.UUID, in integrations.CreateInput) (models.Integration, error)
	List(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error)
	Update(ctx context.Context, orgID, key uuid.UUID, patch models.IntegrationPatch) (models.Integration, error)
	Delete(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
}

// GitHubInstaller runs the GitHub App install flow.
type GitHubInstaller interface {
	Start(orgID uuid.UUID, userID, returnTo string) (githubapp.Install, error)
	Complete(ctx context.Context, cb githubapp.Callback) (returnTo string, err error)
}

// Analytics looks up a metric plugin by name.
type Analytics interface {
	Get(metric string) (analytics.Plugin, error)
}

// Deps is everything the HTTP layer needs.
type Deps struct {
	Logger        zerolog.Logger
	AllowedOrigin string // CORS origin of the browser UI
	FrontendURL   string // base URL for redirects back to the UI
	Keyfunc       jwt.Keyfunc
	Integrations  Integrations
	GitHub        GitHubInstaller
	Analytics     Analytics
	Metrics       *prometheus.Registry // HTTP metrics are registered here and served at /metrics
}

type server struct {
	Deps
	metrics *httpMetrics
}

// New returns the service's HTTP handler.
func New(deps Deps) (http.Handler, error) {
	metrics, err := newHTTPMetrics(deps.Metrics)
	if err != nil {
		return nil, fmt.Errorf("register http metrics: %w", err)
	}
	s := &server{Deps: deps, metrics: metrics}
	return s.routes(), nil
}

func (s *server) routes() http.Handler {
	r := chi.NewRouter()

	// Order matters:
	//   - the logger goes into the context first so everything below can enrich it;
	//   - CORS runs before auth so preflight requests are never rejected;
	//   - the request ID comes before the request logger so every line carries it;
	//   - the request logger wraps Recoverer so panics are logged.
	// chi's RealIP is deliberately absent: it trusts client-supplied X-Forwarded-For.
	// If client IPs are ever needed behind a proxy, use middleware.ClientIPFromXFF.
	r.Use(withLogger(s.Logger))
	r.Use(cors(s.AllowedOrigin))
	r.Use(middleware.RequestID)
	r.Use(logRequests)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(requestTimeout))
	r.Use(s.metrics.middleware)

	r.Get("/healthz", s.healthz)
	r.Handle("/metrics", promhttp.HandlerFor(s.Metrics, promhttp.HandlerOpts{}))
	// GitHub redirects the browser here; the signed state stands in for the JWT.
	r.Get("/v1/auth/github", s.completeGitHubInstall)

	r.Group(func(r chi.Router) {
		r.Use(authenticate(s.Keyfunc))

		r.Post("/v1/auth/github", s.startGitHubInstall)

		r.Get("/v1/integrations/providers", s.listProviders)
		r.Get("/v1/integrations", s.listIntegrations)
		r.Post("/v1/integrations", s.createIntegration)
		r.Put("/v1/integrations/{key}", s.updateIntegration)
		r.Delete("/v1/integrations/{key}", s.deleteIntegration)

		r.Get("/v1/analytics", s.getAnalytics)
	})

	return r
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": ServiceName})
}
