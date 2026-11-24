package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// principal is the caller, taken from a verified JWT.
type principal struct {
	OrgID  uuid.UUID
	UserID string
}

type principalKey struct{}

// principalFrom returns the caller set by authenticate. Routes behind authenticate always
// have one; the handler would never run otherwise.
func principalFrom(ctx context.Context) principal {
	p, _ := ctx.Value(principalKey{}).(principal)
	return p
}

// authenticate verifies the Bearer JWT and puts the caller in the request context. The
// org comes only from the token's organizationId claim, never from request input, so
// every handler below is scoped to the caller's org.
func authenticate(keyfunc jwt.Keyfunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || tokenStr == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			claims := jwt.MapClaims{}
			token, err := jwt.ParseWithClaims(tokenStr, claims, keyfunc, jwt.WithExpirationRequired())
			if err != nil || !token.Valid {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			rawOrgID, _ := claims["organizationId"].(string)
			userID, _ := claims["userId"].(string)
			if rawOrgID == "" || userID == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			orgID, err := uuid.Parse(rawOrgID)
			if err != nil {
				writeCode(w, http.StatusBadRequest, codeInvalidOrgID)
				return
			}

			log := zerolog.Ctx(r.Context()).With().Str("org_id", rawOrgID).Str("user_id", userID).Logger()
			ctx := context.WithValue(log.WithContext(r.Context()), principalKey{}, principal{OrgID: orgID, UserID: userID})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// withLogger puts the base logger in every request context, so later middleware and
// handlers can enrich it with zerolog.Ctx.
func withLogger(logger zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(logger.WithContext(r.Context())))
		})
	}
}

// cors allows the browser UI at allowedOrigin and answers preflight requests directly.
func cors(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", allowedOrigin)
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// logRequests logs every completed request and echoes the request ID in X-Request-Id
// (chi's RequestID middleware only stores it in the context).
func logRequests(next http.Handler) http.Handler {
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
