package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httptest.NewServer(newRouter(logger))
}

func TestPing(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/ping", "application/json", strings.NewReader(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["message"] != "pong: hello" {
		t.Errorf("expected 'pong: hello', got %q", body["message"])
	}
	if body["service"] != serviceName {
		t.Errorf("expected service %q, got %q", serviceName, body["service"])
	}
}

func TestPingEmptyBody(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/ping", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["message"] != "pong: " {
		t.Errorf("expected 'pong: ', got %q", body["message"])
	}
}

func TestPingInvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/ping", "application/json", strings.NewReader(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
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
