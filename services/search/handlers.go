package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": serviceName,
	})
}

func (a *app) searchHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	orgID := orgIDFromCtx(r)
	query := r.URL.Query().Get("q")

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	results, err := a.repository.Search(r.Context(), SearchRequest{
		OrgID: orgID,
		Query: query,
		Limit: limit,
	})
	if err != nil {
		log.Error().Err(err).Msg("search failed")
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
	log := zerolog.Ctx(r.Context())
	orgID := orgIDFromCtx(r)

	flags, err := a.repository.ListFlags(r.Context(), orgID, FlagFilters{
		Source:      r.URL.Query().Get("source"),
		Environment: r.URL.Query().Get("environment"),
	})
	if err != nil {
		log.Error().Err(err).Msg("list flags failed")
		writeError(w, http.StatusInternalServerError, "failed to list flags")
		return
	}

	writeJSON(w, http.StatusOK, FlagsResponse{
		OrgID: orgID,
		Count: len(flags),
		Flags: flags,
	})
}

func (a *app) flagDetailHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	orgID := orgIDFromCtx(r)
	flagKey := chi.URLParam(r, "flag_key")

	flag, err := a.repository.GetFlag(r.Context(), orgID, flagKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "flag not found")
			return
		}
		log.Error().Err(err).Str("flag_key", flagKey).Msg("get flag failed")
		writeError(w, http.StatusInternalServerError, "failed to get flag")
		return
	}

	writeJSON(w, http.StatusOK, FlagDetailResponse{OrgID: orgID, Flag: flag})
}

func (a *app) flagHistoryHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	orgID := orgIDFromCtx(r)
	flagKey := chi.URLParam(r, "flag_key")

	page, err := parsePage(r.URL.Query().Get("page"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := a.repository.GetFlagHistory(r.Context(), orgID, flagKey, Pagination{Page: page, Limit: limit})
	if err != nil {
		log.Error().Err(err).Str("flag_key", flagKey).Msg("get flag history failed")
		writeError(w, http.StatusInternalServerError, "failed to get flag history")
		return
	}

	writeJSON(w, http.StatusOK, HistoryResponse{
		OrgID:   orgID,
		FlagKey: flagKey,
		Page:    page,
		Count:   len(entries),
		Entries: entries,
	})
}

func (a *app) timelineHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	orgID := orgIDFromCtx(r)

	page, err := parsePage(r.URL.Query().Get("page"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := a.repository.GetTimeline(r.Context(), orgID, Pagination{Page: page, Limit: limit})
	if err != nil {
		log.Error().Err(err).Msg("get timeline failed")
		writeError(w, http.StatusInternalServerError, "failed to get timeline")
		return
	}

	writeJSON(w, http.StatusOK, TimelineResponse{
		OrgID:   orgID,
		Page:    page,
		Count:   len(entries),
		Entries: entries,
	})
}

// orgIDFromCtx returns the org ID stored in context by orgIDMiddleware.
func orgIDFromCtx(r *http.Request) string {
	if v, ok := r.Context().Value(orgIDKey).(string); ok && v != "" {
		return v
	}
	return defaultOrgID
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encode error is intentionally ignored: WriteHeader has already been
	// called so the status cannot change, and a partial write is better than
	// leaving the client with no response body at all.
	_ = json.NewEncoder(w).Encode(payload)
}
