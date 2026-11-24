package main

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// corsMiddleware sets CORS headers and short-circuits OPTIONS preflight requests.
// allowedOrigin is the exact origin the browser UI runs on (e.g. http://localhost:3001).
func corsMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (a *app) router() http.Handler {
	r := chi.NewRouter()

	// Inject the base logger into every request context so downstream
	// middleware and handlers can enrich it via zerolog.Ctx(ctx).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := a.logger.WithContext(r.Context())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	// CORS must run before JWT so preflight OPTIONS requests are never blocked.
	r.Use(corsMiddleware(a.config.AllowedOrigin))

	// Order matters: the request ID comes first so every log line carries it, and the
	// logger wraps Recoverer so panics are logged. chi's RealIP is deliberately absent:
	// it trusts client-supplied X-Forwarded-For / X-Real-IP headers. If client IPs are
	// ever needed behind a proxy, use middleware.ClientIPFromXFF with the proxy CIDRs.
	r.Use(middleware.RequestID)
	r.Use(requestLoggerMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(prometheusMiddleware)

	// Infrastructure routes — no org ID required.
	r.Get("/healthz", healthzHandler)
	r.Handle("/metrics", promhttp.Handler())
	r.Get("/v1/auth/github", a.githubAuthCallbackHandler)

	// API routes — JWT required.
	r.Group(func(r chi.Router) {
		r.Use(a.jwtMiddleware)

		// Integrations API
		r.Get("/v1/integrations/providers", a.providersHandler)
		r.Post("/v1/integrations", a.createIntegrationHandler)
		r.Get("/v1/integrations", a.listIntegrationsHandler)
		r.Put("/v1/integrations/{key}", a.updateIntegrationHandler)
		r.Delete("/v1/integrations/{key}", a.deleteIntegrationHandler)
		r.Post("/v1/auth/github", a.startGitHubAuthHandler)

		// Analytics API
		r.Get("/v1/analytics", a.analyticsHandler)
	})

	return r
}
