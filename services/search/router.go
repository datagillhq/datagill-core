package main

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

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

	// Middleware order matches the spec in IMPLEMENTATION_PLAN.md §1C.2.
	r.Use(middleware.RequestID)
	r.Use(requestLoggerMiddleware)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(orgIDMiddleware)
	r.Use(prometheusMiddleware)

	r.Get("/healthz", healthzHandler)
	r.Handle("/metrics", promhttp.Handler())

	r.Get("/api/v1/search", a.searchHandler)
	r.Get("/api/v1/flags", a.flagsHandler)

	return r
}
