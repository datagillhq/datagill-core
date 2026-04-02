package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/services/search/analytics"
)

// analyticsHandler serves GET /v1/analytics?metric=...
func (a *app) analyticsHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())

	orgID, err := uuid.Parse(orgIDFromCtx(r))
	if err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_ORG_ID")
		return
	}

	q := r.URL.Query()

	metric := q.Get("metric")
	if metric == "" {
		writeCode(w, http.StatusBadRequest, "ANALYTICS_MISSING_METRIC")
		return
	}

	fromStr := q.Get("from")
	toStr := q.Get("to")
	if fromStr == "" || toStr == "" {
		writeCode(w, http.StatusBadRequest, "ANALYTICS_MISSING_DATE_RANGE")
		return
	}

	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		writeCode(w, http.StatusBadRequest, "ANALYTICS_INVALID_DATE_FORMAT")
		return
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		writeCode(w, http.StatusBadRequest, "ANALYTICS_INVALID_DATE_FORMAT")
		return
	}

	page := 1
	if s := q.Get("page"); s != "" {
		if page, err = strconv.Atoi(s); err != nil || page < 1 {
			writeCode(w, http.StatusBadRequest, "ANALYTICS_INVALID_PAGE")
			return
		}
	}

	limit := 25
	if s := q.Get("limit"); s != "" {
		if limit, err = strconv.Atoi(s); err != nil || limit < 1 {
			writeCode(w, http.StatusBadRequest, "ANALYTICS_INVALID_LIMIT")
			return
		}
		if limit > analytics.MaxLimit {
			limit = analytics.MaxLimit
		}
	}

	plugin, err := a.analyticsRegistry.Get(metric)
	if err != nil {
		if errors.Is(err, analytics.ErrUnknownMetric) {
			writeCode(w, http.StatusBadRequest, "ANALYTICS_UNKNOWN_METRIC")
			return
		}
		log.Error().Err(err).Msg("registry lookup failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	result, err := plugin.Execute(r.Context(), analytics.Query{
		OrgID: orgID,
		From:  from,
		To:    to,
		Team:  q.Get("team"),
		Actor: q.Get("actor"),
		Page:  page,
		Limit: limit,
	})
	if err != nil {
		log.Error().Err(err).Str("metric", metric).Msg("plugin execute failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"orgId":  orgID,
		"metric": metric,
		"from":   from,
		"to":     to,
		"data":   result,
	})
}
