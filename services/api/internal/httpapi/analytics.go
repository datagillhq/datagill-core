package httpapi

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/services/api/analytics"
)

const (
	defaultPage  = 1
	defaultLimit = 25
)

// getAnalytics serves GET /v1/analytics?metric=<name>&from=<RFC3339>&to=<RFC3339>.
func (s *server) getAnalytics(w http.ResponseWriter, r *http.Request) {
	orgID := principalFrom(r.Context()).OrgID
	log := zerolog.Ctx(r.Context())

	metric, query, code := parseAnalyticsQuery(orgID, r.URL.Query())
	if code != "" {
		log.Warn().Str("code", code).Msg("analytics request rejected")
		writeCode(w, http.StatusBadRequest, code)
		return
	}

	plugin, err := s.Analytics.Get(metric)
	if errors.Is(err, analytics.ErrUnknownMetric) {
		log.Warn().Str("code", "ANALYTICS_UNKNOWN_METRIC").Str("metric", metric).Msg("analytics request rejected")
		writeCode(w, http.StatusBadRequest, "ANALYTICS_UNKNOWN_METRIC")
		return
	}
	if err != nil {
		writeInternal(w, r, err, "analytics registry lookup failed")
		return
	}

	result, err := plugin.Execute(r.Context(), query)
	if err != nil {
		writeInternal(w, r, err, "analytics metric "+metric+" failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"orgId": orgID,
		"from":  query.From,
		"to":    query.To,
		"data":  map[string]any{metric: result},
	})
}

// parseAnalyticsQuery validates the query string. On failure it returns the error code to
// send back.
func parseAnalyticsQuery(orgID uuid.UUID, q url.Values) (metric string, query analytics.Query, code string) {
	metric = q.Get("metric")
	if metric == "" {
		return "", analytics.Query{}, "ANALYTICS_MISSING_METRIC"
	}

	if q.Get("from") == "" || q.Get("to") == "" {
		return "", analytics.Query{}, "ANALYTICS_MISSING_DATE_RANGE"
	}
	from, errFrom := time.Parse(time.RFC3339, q.Get("from"))
	to, errTo := time.Parse(time.RFC3339, q.Get("to"))
	if errFrom != nil || errTo != nil {
		return "", analytics.Query{}, "ANALYTICS_INVALID_DATE_FORMAT"
	}
	// An inverted range matches nothing; say so instead of returning an empty result that
	// looks like "no activity".
	if from.After(to) {
		return "", analytics.Query{}, "ANALYTICS_INVALID_DATE_RANGE"
	}

	page, ok := positiveInt(q.Get("page"), defaultPage)
	if !ok {
		return "", analytics.Query{}, "ANALYTICS_INVALID_PAGE"
	}
	limit, ok := positiveInt(q.Get("limit"), defaultLimit)
	if !ok {
		return "", analytics.Query{}, "ANALYTICS_INVALID_LIMIT"
	}
	limit = min(limit, analytics.MaxLimit)
	// Repositories compute OFFSET as (page-1)*limit. A page so large that this overflows
	// would wrap to a negative OFFSET, which Postgres rejects, turning bad input into a 500.
	if page-1 > math.MaxInt/limit {
		return "", analytics.Query{}, "ANALYTICS_INVALID_PAGE"
	}

	return metric, analytics.Query{
		OrgID: orgID,
		From:  from,
		To:    to,
		Team:  q.Get("team"),
		Actor: q.Get("actor"),
		Page:  page,
		Limit: limit,
	}, ""
}

// positiveInt parses s as an integer >= 1, or returns fallback when s is empty.
func positiveInt(s string, fallback int) (int, bool) {
	if s == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 1
}
