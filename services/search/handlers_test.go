package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	resp := get(t, srv.URL+"/healthz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", body["status"])
	}
	if body["service"] != serviceName {
		t.Errorf("expected service %q, got %q", serviceName, body["service"])
	}
}
