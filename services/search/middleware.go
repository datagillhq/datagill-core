package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

type contextKey string

const orgIDKey contextKey = "orgID"

// orgIDMiddleware extracts X-Org-Id from the request header, stores it in the
// context (for handlers), and enriches the zerolog context logger with the
// org_id field (for structured log filtering by tenant in Loki/Grafana).
func orgIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orgID := strings.TrimSpace(r.Header.Get("X-Org-Id"))
		if orgID == "" {
			orgID = defaultOrgID
		}

		log := zerolog.Ctx(r.Context()).With().Str("org_id", orgID).Logger()
		ctx := context.WithValue(log.WithContext(r.Context()), orgIDKey, orgID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requestLoggerMiddleware logs each completed request with method, path,
// request_id, status, and duration_ms. Must be placed after RequestID so the
// ID is available via chimiddleware.GetReqID. Also sets X-Request-Id on the
// response (chi's RequestID middleware only stores the ID in context).
func requestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := chimiddleware.GetReqID(r.Context())
		w.Header().Set("X-Request-Id", requestID)

		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		zerolog.Ctx(r.Context()).Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("request_id", requestID).
			Int("status", ww.Status()).
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Msg("request")
	})
}
