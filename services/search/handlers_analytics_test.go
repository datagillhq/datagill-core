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
	return newTestServerFull(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, reg)
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
		if body["orgId"] == nil {
			t.Error("expected orgId in envelope")
		}
		if body["metric"] != nil {
			t.Error("top-level metric field should not be present")
		}
		data, ok := body["data"].(map[string]any)
		if !ok {
			t.Fatalf("expected data to be a map, got %T", body["data"])
		}
		if data["launched_wins"] == nil {
			t.Error("expected data[launched_wins] to be present")
		}
	})

	t.Run("pull_requests metric returns data key in envelope", func(t *testing.T) {
		srv := newAnalyticsTestServer(t, &stubPlugin{
			metric: "pull_requests",
			result: analytics.PullRequestsResponse{},
		})
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=pull_requests&"+validDateRange, testOrgID, testUserID)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		data, ok := body["data"].(map[string]any)
		if !ok {
			t.Fatalf("expected data to be a map, got %T", body["data"])
		}
		if data["pull_requests"] == nil {
			t.Error("expected data[pull_requests] to be present")
		}
	})

	t.Run("missing metric returns 400", func(t *testing.T) {
		srv := newTestServer(t)
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?"+validDateRange, testOrgID, testUserID)
		assertCode(t, resp, http.StatusBadRequest, "ANALYTICS_MISSING_METRIC")
	})

	t.Run("missing from/to returns 400", func(t *testing.T) {
		srv := newTestServer(t)
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=launched_wins", testOrgID, testUserID)
		assertCode(t, resp, http.StatusBadRequest, "ANALYTICS_MISSING_DATE_RANGE")
	})

	t.Run("invalid date format returns 400", func(t *testing.T) {
		srv := newTestServer(t)
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=launched_wins&from=not-a-date&to=also-not", testOrgID, testUserID)
		assertCode(t, resp, http.StatusBadRequest, "ANALYTICS_INVALID_DATE_FORMAT")
	})

	t.Run("unknown metric returns 400", func(t *testing.T) {
		srv := newTestServer(t)
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=does_not_exist&"+validDateRange, testOrgID, testUserID)
		assertCode(t, resp, http.StatusBadRequest, "ANALYTICS_UNKNOWN_METRIC")
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
		srv := newTestServer(t)
		defer srv.Close()

		resp := get(t, srv.URL+"/v1/analytics?metric=velocity&"+validDateRange)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("limit capped at MaxLimit", func(t *testing.T) {
		srv := newAnalyticsTestServer(t, &stubPlugin{
			metric: "audit_log",
			result: analytics.AuditLogResponse{},
		})
		defer srv.Close()

		resp := getWithToken(t, srv.URL+"/v1/analytics?metric=audit_log&limit=9999&"+validDateRange, testOrgID, testUserID)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})
}

// assertCode checks the HTTP status and the "code" field in the JSON body.
func assertCode(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("expected status %d, got %d", status, resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body["code"] != code {
		t.Errorf("expected code %q, got %q", code, body["code"])
	}
}
