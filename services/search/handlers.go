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
		a.logger.Error().Err(err).Str("org_id", orgID).Msg("search failed")
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
		a.logger.Error().Err(err).Str("org_id", orgID).Msg("list flags failed")
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
