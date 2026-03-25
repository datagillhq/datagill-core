package main

import (
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

func TestOrgIDHeaderRequired(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()
	resp := get(t, srv.URL+"/flags")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
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
