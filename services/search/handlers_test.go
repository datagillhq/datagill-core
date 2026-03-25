package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// --- /healthz ---

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
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

// --- /search ---

func TestSearch(t *testing.T) {
	result := SearchResult{FlagKey: "checkout-redesign", Title: "Checkout", Score: 1.0, LastEventAt: time.Now()}
	srv := newTestServer(t, &stubRepo{searchResults: []SearchResult{result}})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/search?q=checkout", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 1 || body.Results[0].FlagKey != "checkout-redesign" {
		t.Errorf("unexpected search response: %+v", body)
	}
}

func TestSearchInvalidLimit(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()
	for _, limit := range []string{"0", "-1", "101", "abc"} {
		resp := get(t, srv.URL+"/search?limit="+limit)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%q: expected 400, got %d", limit, resp.StatusCode)
		}
	}
}

func TestSearchRepositoryError(t *testing.T) {
	srv := newTestServer(t, &stubRepo{searchErr: repoErr})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/search?q=test", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// --- /flags ---

func TestListFlags(t *testing.T) {
	flags := []FlagSummary{
		{FlagKey: "flag-a", Source: "launchdarkly"},
		{FlagKey: "flag-b", Source: "unleash"},
	}
	srv := newTestServer(t, &stubRepo{flags: flags})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/flags", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body FlagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 {
		t.Fatalf("expected 2 flags, got %d", body.Count)
	}
}

func TestListFlagsRepositoryError(t *testing.T) {
	srv := newTestServer(t, &stubRepo{flagsErr: repoErr})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/flags", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// --- /flags/{flag_key} ---

func TestGetFlagDetail(t *testing.T) {
	detail := FlagDetail{FlagKey: "checkout-redesign", Name: "Checkout Redesign"}
	srv := newTestServer(t, &stubRepo{flagDetail: detail})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/flags/checkout-redesign", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body FlagDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Flag.FlagKey != "checkout-redesign" {
		t.Errorf("expected checkout-redesign, got %q", body.Flag.FlagKey)
	}
}

func TestGetFlagDetailNotFound(t *testing.T) {
	srv := newTestServer(t, &stubRepo{flagDetailErr: ErrNotFound})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/flags/does-not-exist", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetFlagDetailRepositoryError(t *testing.T) {
	srv := newTestServer(t, &stubRepo{flagDetailErr: repoErr})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/flags/any-flag", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// --- /flags/{flag_key}/history ---

func TestGetFlagHistory(t *testing.T) {
	entries := []ChangelogEntry{
		{ID: "e1", FlagKey: "checkout-redesign", EventType: "flag_toggled_on"},
	}
	srv := newTestServer(t, &stubRepo{history: entries})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/flags/checkout-redesign/history", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body HistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 1 {
		t.Errorf("expected 1 entry, got %d", body.Count)
	}
}

func TestGetFlagHistoryInvalidPage(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/flags/checkout-redesign/history?page=0", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGetFlagHistoryRepositoryError(t *testing.T) {
	srv := newTestServer(t, &stubRepo{historyErr: repoErr})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/flags/any-flag/history", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// --- /timeline ---

func TestGetTimeline(t *testing.T) {
	entries := []ChangelogEntry{
		{ID: "e1", FlagKey: "flag-a", EventType: "flag_toggled_on"},
		{ID: "e2", FlagKey: "flag-b", EventType: "flag_created"},
	}
	srv := newTestServer(t, &stubRepo{timeline: entries})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/timeline", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body TimelineResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 {
		t.Errorf("expected 2 entries, got %d", body.Count)
	}
}

func TestGetTimelineRepositoryError(t *testing.T) {
	srv := newTestServer(t, &stubRepo{timelineErr: repoErr})
	defer srv.Close()
	resp := getWithOrgID(t, srv.URL+"/timeline", testOrgID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}
