package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestRequestIDHeaderIsSet(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()
	resp := get(t, srv.URL+"/healthz")
	defer resp.Body.Close()
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("expected X-Request-Id response header to be set")
	}
}

func TestOrgIDDefaultsWhenHeaderMissing(t *testing.T) {
	srv := newTestServer(t, &stubRepo{flags: []FlagSummary{{FlagKey: "f1"}}})
	defer srv.Close()
	resp := get(t, srv.URL+"/flags")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body FlagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.OrgID != defaultOrgID {
		t.Errorf("expected orgId %q, got %q", defaultOrgID, body.OrgID)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()
	resp := get(t, srv.URL+"/metrics")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
