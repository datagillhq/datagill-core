package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/datagill/datagill-core/services/api/analytics"
)

const validDateRange = "from=2025-01-01T00:00:00Z&to=2025-02-01T00:00:00Z"

func newAnalyticsServer(t *testing.T, plugin analytics.Plugin) string {
	t.Helper()
	reg := analytics.NewRegistry()
	if err := reg.Register(plugin); err != nil {
		t.Fatal(err)
	}
	deps := testDeps()
	deps.Analytics = reg
	return newTestServer(t, deps).URL
}

func TestGetAnalytics(t *testing.T) {
	t.Run("wraps the result in the envelope", func(t *testing.T) {
		plugin := &stubPlugin{metric: "launched_wins", result: analytics.LaunchedWinsResponse{Wins: []analytics.LaunchedWinEntry{}}}
		url := newAnalyticsServer(t, plugin)

		resp := do(t, http.MethodGet, url+"/v1/analytics?metric=launched_wins&team=core&"+validDateRange, validToken(t), nil)
		assertStatus(t, resp, http.StatusOK)

		body := decodeJSON(t, resp)
		if body["orgId"] != testOrgID || body["from"] == nil || body["to"] == nil {
			t.Fatalf("unexpected envelope: %v", body)
		}
		if body["metric"] != nil {
			t.Error("top-level metric field should not be present")
		}
		data, _ := body["data"].(map[string]any)
		if data["launched_wins"] == nil {
			t.Fatalf("expected data.launched_wins, got %v", body["data"])
		}
		if plugin.query.OrgID.String() != testOrgID || plugin.query.Team != "core" || plugin.query.Page != 1 || plugin.query.Limit != 25 {
			t.Fatalf("unexpected query passed to plugin: %+v", plugin.query)
		}
	})

	t.Run("caps limit at MaxLimit", func(t *testing.T) {
		plugin := &stubPlugin{metric: "audit_log", result: analytics.AuditLogResponse{}}
		url := newAnalyticsServer(t, plugin)

		assertStatus(t, do(t, http.MethodGet, url+"/v1/analytics?metric=audit_log&limit=9999&page=3&"+validDateRange, validToken(t), nil), http.StatusOK)
		if plugin.query.Limit != analytics.MaxLimit || plugin.query.Page != 3 {
			t.Fatalf("expected limit %d page 3, got %+v", analytics.MaxLimit, plugin.query)
		}
	})

	t.Run("from equal to to is allowed", func(t *testing.T) {
		url := newAnalyticsServer(t, &stubPlugin{metric: "velocity", result: analytics.VelocityResponse{}})
		assertStatus(t, do(t, http.MethodGet, url+"/v1/analytics?metric=velocity&from=2026-11-01T00:00:00Z&to=2026-11-01T00:00:00Z", validToken(t), nil), http.StatusOK)
	})

	t.Run("plugin error is a 500", func(t *testing.T) {
		url := newAnalyticsServer(t, &stubPlugin{metric: "velocity", err: errors.New("db error")})
		assertCode(t, do(t, http.MethodGet, url+"/v1/analytics?metric=velocity&"+validDateRange, validToken(t), nil), http.StatusInternalServerError, codeInternal)
	})

	t.Run("no auth", func(t *testing.T) {
		srv := newTestServer(t, testDeps())
		assertStatus(t, do(t, http.MethodGet, srv.URL+"/v1/analytics?metric=velocity&"+validDateRange, "", nil), http.StatusUnauthorized)
	})
}

func TestGetAnalytics_Validation(t *testing.T) {
	tests := []struct {
		query string
		code  string
	}{
		{validDateRange, "ANALYTICS_MISSING_METRIC"},
		{"metric=velocity", "ANALYTICS_MISSING_DATE_RANGE"},
		{"metric=velocity&from=2025-01-01T00:00:00Z", "ANALYTICS_MISSING_DATE_RANGE"},
		{"metric=velocity&from=not-a-date&to=also-not", "ANALYTICS_INVALID_DATE_FORMAT"},
		{"metric=velocity&page=0&" + validDateRange, "ANALYTICS_INVALID_PAGE"},
		{"metric=velocity&page=x&" + validDateRange, "ANALYTICS_INVALID_PAGE"},
		{"metric=velocity&limit=-1&" + validDateRange, "ANALYTICS_INVALID_LIMIT"},
		{"metric=velocity&from=2026-11-02T00:00:00Z&to=2026-11-01T00:00:00Z", "ANALYTICS_INVALID_DATE_RANGE"},
		{"metric=audit_log&page=9223372036854775807&limit=100&" + validDateRange, "ANALYTICS_INVALID_PAGE"},
		{"metric=does_not_exist&" + validDateRange, "ANALYTICS_UNKNOWN_METRIC"},
	}
	srv := newTestServer(t, testDeps())
	for _, tt := range tests {
		t.Run(tt.code+"/"+tt.query, func(t *testing.T) {
			assertCode(t, do(t, http.MethodGet, srv.URL+"/v1/analytics?"+tt.query, validToken(t), nil), http.StatusBadRequest, tt.code)
		})
	}
}
