package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	pingv1 "github.com/datagill/datagill-core/shared/gen/ping/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func pingHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		req := &pingv1.PingRequest{}
		if len(body) > 0 {
			if err := protojson.Unmarshal(body, req); err != nil {
				http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
				return
			}
		}

		resp := &pingv1.PingResponse{
			Message: fmt.Sprintf("pong: %s", req.GetMessage()),
			Service: serviceName,
		}

		out, err := protojson.Marshal(resp)
		if err != nil {
			http.Error(w, "failed to marshal response", http.StatusInternalServerError)
			return
		}

		logger.Info("ping", "message", req.GetMessage())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(out) //nolint:errcheck
	}
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": serviceName,
	})
}

func readyzHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ready",
		"service": serviceName,
	})
}

func (a *app) searchHandler(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 50")
		return
	}

	orgID := orgIDFromRequest(r.Header.Get("X-Org-Id"))
	query := r.URL.Query().Get("q")

	results, err := a.repository.Search(r.Context(), SearchRequest{
		OrgID: orgID,
		Query: query,
		Limit: limit,
	})
	if err != nil {
		a.logger.Error("search failed", "err", err, "org_id", orgID)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}

	writeJSON(w, http.StatusOK, SearchResponse{
		Query:   query,
		OrgID:   orgID,
		Count:   len(results),
		Results: results,
	})
}

func (a *app) flagsHandler(w http.ResponseWriter, r *http.Request) {
	orgID := orgIDFromRequest(r.Header.Get("X-Org-Id"))

	flags, err := a.repository.ListFlags(r.Context(), orgID)
	if err != nil {
		a.logger.Error("list flags failed", "err", err, "org_id", orgID)
		writeError(w, http.StatusInternalServerError, "failed to list flags")
		return
	}

	writeJSON(w, http.StatusOK, FlagsResponse{
		OrgID: orgID,
		Count: len(flags),
		Flags: flags,
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
