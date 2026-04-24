package main

import (
	"encoding/json"
	"net/http"
)

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": serviceName,
	})
}

// orgIDFromCtx returns the org ID stored in context by jwtMiddleware.
// The type assertion is always safe: jwtMiddleware returns 401 before any
// API handler runs, so the value is guaranteed to be present.
func orgIDFromCtx(r *http.Request) string {
	v, _ := r.Context().Value(orgIDKey).(string)
	return v
}

// userIDFromCtx returns the user ID stored in context by jwtMiddleware.
func userIDFromCtx(r *http.Request) string {
	v, _ := r.Context().Value(userIDKey).(string)
	return v
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encode error is intentionally ignored: WriteHeader has already been
	// called so the status cannot change, and a partial write is better than
	// leaving the client with no response body at all.
	_ = json.NewEncoder(w).Encode(payload)
}
