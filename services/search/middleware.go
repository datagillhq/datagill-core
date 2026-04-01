package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
)

type contextKey string

const (
	orgIDKey  contextKey = "orgID"
	userIDKey contextKey = "userID"
)

// jwtMiddleware validates the Bearer JWT in the Authorization header, then
// extracts the organizationId and userId claims and injects them into the
// request context. Requests with missing, expired, or tampered tokens are
// rejected with 401.
func (a *app) jwtMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		tokenStr, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok || tokenStr == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		token, err := jwt.Parse(tokenStr, a.jwtKeyfunc, jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		orgID, _ := claims["organizationId"].(string)
		userID, _ := claims["userId"].(string)
		if orgID == "" || userID == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		log := zerolog.Ctx(r.Context()).With().Str("org_id", orgID).Str("user_id", userID).Logger()
		ctx := context.WithValue(log.WithContext(r.Context()), orgIDKey, orgID)
		ctx = context.WithValue(ctx, userIDKey, userID)

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

// prometheusMiddleware records request count and latency, labeled by the
// matched chi route pattern so /flags/{flag_key} is one label, not N.
func prometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(sw, r)

		pattern := chi.RouteContext(r.Context()).RoutePattern()
		if pattern == "" {
			pattern = r.URL.Path
		}

		httpRequestsTotal.WithLabelValues(r.Method, pattern, strconv.Itoa(sw.status)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, pattern).Observe(time.Since(start).Seconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(status int) {
	sw.status = status
	sw.ResponseWriter.WriteHeader(status)
}
