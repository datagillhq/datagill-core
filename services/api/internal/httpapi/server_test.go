package httpapi

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, testDeps())
	resp := do(t, http.MethodGet, srv.URL+"/healthz", "", nil)
	assertStatus(t, resp, http.StatusOK)

	body := decodeJSON(t, resp)
	if body["status"] != "ok" || body["service"] != ServiceName {
		t.Fatalf("unexpected body: %v", body)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("expected X-Request-Id response header")
	}
}

func TestCORSPreflight(t *testing.T) {
	srv := newTestServer(t, testDeps())
	resp := do(t, http.MethodOptions, srv.URL+"/v1/integrations", "", nil)
	assertStatus(t, resp, http.StatusNoContent)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:3001" {
		t.Fatalf("unexpected allowed origin %q", got)
	}
}

func TestAuthenticate(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	tests := []struct {
		name   string
		token  string
		status int
	}{
		{"missing token", "", http.StatusUnauthorized},
		{"garbage token", "not-a-jwt", http.StatusUnauthorized},
		{"valid token", validToken(t), http.StatusOK},
		{"expired token", signToken(t, jwt.MapClaims{"organizationId": testOrgID, "userId": testUserID, "exp": time.Now().Add(-time.Minute).Unix()}), http.StatusUnauthorized},
		{"no expiry", signToken(t, jwt.MapClaims{"organizationId": testOrgID, "userId": testUserID}), http.StatusUnauthorized},
		{"missing org claim", signToken(t, jwt.MapClaims{"userId": testUserID, "exp": future}), http.StatusUnauthorized},
		{"missing user claim", signToken(t, jwt.MapClaims{"organizationId": testOrgID, "exp": future}), http.StatusUnauthorized},
		{"org claim not a uuid", signToken(t, jwt.MapClaims{"organizationId": "acme", "userId": testUserID, "exp": future}), http.StatusBadRequest},
	}
	srv := newTestServer(t, testDeps())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, do(t, http.MethodGet, srv.URL+"/v1/integrations", tt.token, nil), tt.status)
		})
	}
}

func TestMetrics(t *testing.T) {
	srv := newTestServer(t, testDeps())
	do(t, http.MethodGet, srv.URL+"/healthz", "", nil)
	do(t, http.MethodGet, srv.URL+"/does-not-exist-123", "", nil)

	resp := do(t, http.MethodGet, srv.URL+"/metrics", "", nil)
	assertStatus(t, resp, http.StatusOK)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	for _, want := range []string{
		`datagill_api_http_requests_total{handler="/healthz",method="GET",status_code="200"} 1`,
		`datagill_api_http_requests_total{handler="unmatched",method="GET",status_code="404"} 1`,
		`datagill_api_http_request_duration_seconds_count{handler="/healthz",method="GET"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected metrics to contain %s", want)
		}
	}
	if strings.Contains(body, "does-not-exist-123") {
		t.Error("unmatched paths must not become label values")
	}
}
