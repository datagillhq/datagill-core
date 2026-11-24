package main

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/services/search/analytics"
)

type analyticsRequest struct {
	metric string
	query  analytics.Query
}

// parseAnalyticsQuery validates and parses the analytics query params.
// Returns a non-empty error code string on any validation failure.
func parseAnalyticsQuery(orgID uuid.UUID, q url.Values) (analyticsRequest, string) {
	metric := q.Get("metric")
	if metric == "" {
		return analyticsRequest{}, "ANALYTICS_MISSING_METRIC"
	}

	fromStr := q.Get("from")
	toStr := q.Get("to")
	if fromStr == "" || toStr == "" {
		return analyticsRequest{}, "ANALYTICS_MISSING_DATE_RANGE"
	}

	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		return analyticsRequest{}, "ANALYTICS_INVALID_DATE_FORMAT"
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		return analyticsRequest{}, "ANALYTICS_INVALID_DATE_FORMAT"
	}
	// An inverted range matches nothing; say so instead of returning an empty result
	// that looks like "no activity".
	if from.After(to) {
		return analyticsRequest{}, "ANALYTICS_INVALID_DATE_RANGE"
	}

	page := 1
	if s := q.Get("page"); s != "" {
		if page, err = strconv.Atoi(s); err != nil || page < 1 {
			return analyticsRequest{}, "ANALYTICS_INVALID_PAGE"
		}
	}

	limit := 25
	if s := q.Get("limit"); s != "" {
		if limit, err = strconv.Atoi(s); err != nil || limit < 1 {
			return analyticsRequest{}, "ANALYTICS_INVALID_LIMIT"
		}
		if limit > analytics.MaxLimit {
			limit = analytics.MaxLimit
		}
	}

	// Repositories compute OFFSET as (page-1)*limit. A page so large that this overflows
	// would wrap to a negative OFFSET, which Postgres rejects, turning bad input into a 500.
	if page-1 > math.MaxInt/limit {
		return analyticsRequest{}, "ANALYTICS_INVALID_PAGE"
	}

	return analyticsRequest{
		metric: metric,
		query: analytics.Query{
			OrgID: orgID,
			From:  from,
			To:    to,
			Team:  q.Get("team"),
			Actor: q.Get("actor"),
			Page:  page,
			Limit: limit,
		},
	}, ""
}

// analyticsHandler serves GET /v1/analytics?metric=...
func (a *app) analyticsHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())

	orgID, err := uuid.Parse(orgIDFromCtx(r))
	if err != nil {
		log.Warn().Str("code", "INVALID_ORG_ID").Msg("analytics request rejected")
		writeCode(w, http.StatusBadRequest, "INVALID_ORG_ID")
		return
	}

	req, errCode := parseAnalyticsQuery(orgID, r.URL.Query())
	if errCode != "" {
		log.Warn().Str("code", errCode).Msg("analytics request rejected")
		writeCode(w, http.StatusBadRequest, errCode)
		return
	}

	plugin, err := a.analyticsRegistry.Get(req.metric)
	if err != nil {
		if errors.Is(err, analytics.ErrUnknownMetric) {
			log.Warn().Str("code", "ANALYTICS_UNKNOWN_METRIC").Str("metric", req.metric).Msg("analytics request rejected")
			writeCode(w, http.StatusBadRequest, "ANALYTICS_UNKNOWN_METRIC")
			return
		}
		log.Error().Err(err).Msg("registry lookup failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	result, err := plugin.Execute(r.Context(), req.query)
	if err != nil {
		log.Error().Err(err).Str("metric", req.metric).Msg("plugin execute failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"orgId": orgID,
		"from":  req.query.From,
		"to":    req.query.To,
		"data":  map[string]any{req.metric: result},
	})
}
