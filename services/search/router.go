package main

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (a *app) router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", healthzHandler)
	r.Get("/readyz", readyzHandler)
	r.Post("/ping", pingHandler(a.logger))

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/search", a.searchHandler)
		api.Get("/flags", a.flagsHandler)
	})

	return r
}
