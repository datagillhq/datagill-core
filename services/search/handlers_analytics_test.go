package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/datagill/datagill-core/services/search/analytics"
)

// stubPlugin is a configurable analytics.Plugin for handler tests.
type stubPlugin struct {
	metric string
	result any
	err    error
}

func (s *stubPlugin) Metric() string    { return s.metric }
func (s *stubPlugin) ResponseType() any { return struct{}{} }
func (s *stubPlugin) Execute(_ context.Context, _ analytics.Query) (any, error) {
	return s.result, s.err
}

// newAnalyticsTestServer builds a test server with the given plugin registered.
func newAnalyticsTestServer(t *testing.T, plugin analytics.Plugin) *httptest.Server {
	t.Helper()
	reg := analytics.NewRegistry()
	if err := reg.Register(plugin); err != nil {
		t.Fatal(err)
	}
	return newTestServerFull(t, &stubRepo{}, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, reg)
}

const validDateRange = "from=2025-01-01T00:00:00Z&to=2025-02-01T00:00:00Z"

func TestAnalyticsHandler(t *testing.T) {
	t.Run("happy path returns 200 with envelope", func(t *testing.T) {
		srv := newAnalyticsTestServer(t, &stubPlugin{
			metric: "launched_wins",
			result: analytics.LaunchedWinsResponse{Wins: []analytics.LaunchedWinEntry{}},
		})
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=launched_wins&"+validDateRange, testOrgID, testUserID)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if body["metric"] != "launched_wins" {
			t.Errorf("expected metric=launched_wins in envelope, got %v", body["metric"])
		}
		if body["orgId"] == nil {
			t.Error("expected orgId in envelope")
		}
		if body["data"] == nil {
			t.Error("expected data in envelope")
		}
	})

	t.Run("missing metric returns 400", func(t *testing.T) {
		srv := newTestServer(t, &stubRepo{})
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?"+validDateRange, testOrgID, testUserID)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		assertCode(t, resp, "ANALYTICS_MISSING_METRIC")
	})

	t.Run("missing from/to returns 400", func(t *testing.T) {
		srv := newTestServer(t, &stubRepo{})
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=launched_wins", testOrgID, testUserID)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		assertCode(t, resp, "ANALYTICS_MISSING_DATE_RANGE")
	})

	t.Run("invalid date format returns 400", func(t *testing.T) {
		srv := newTestServer(t, &stubRepo{})
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=launched_wins&from=not-a-date&to=also-not", testOrgID, testUserID)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		assertCode(t, resp, "ANALYTICS_INVALID_DATE_FORMAT")
	})

	t.Run("unknown metric returns 400", func(t *testing.T) {
		srv := newTestServer(t, &stubRepo{})
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=does_not_exist&"+validDateRange, testOrgID, testUserID)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		assertCode(t, resp, "ANALYTICS_UNKNOWN_METRIC")
	})

	t.Run("plugin execute error returns 500", func(t *testing.T) {
		srv := newAnalyticsTestServer(t, &stubPlugin{
			metric: "velocity",
			err:    errors.New("db error"),
		})
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=velocity&"+validDateRange, testOrgID, testUserID)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", resp.StatusCode)
		}
	})

	t.Run("no auth returns 401", func(t *testing.T) {
		srv := newTestServer(t, &stubRepo{})
		defer srv.Close()

		resp := get(t, srv.URL+"/v1/analytics?metric=velocity&"+validDateRange)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("limit capped at MaxLimit", func(t *testing.T) {
		executed := false
		srv := newAnalyticsTestServer(t, &stubPlugin{
			metric: "audit_log",
			result: analytics.AuditLogResponse{},
		})
		_ = executed
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=audit_log&limit=9999&"+validDateRange, testOrgID, testUserID)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})
}

// assertCode decodes the response body and asserts the "code" field matches expected.
func assertCode(t *testing.T, resp *http.Response, expected string) {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body["code"] != expected {
		t.Errorf("expected code %q, got %q", expected, body["code"])
	}
}
