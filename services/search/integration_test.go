package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// stubRepo lets each test configure exactly the data and errors it needs.
type stubRepo struct {
	searchResults []SearchResult
	searchErr     error
	flags         []FlagSummary
	flagsErr      error
	flagDetail    FlagDetail
	flagDetailErr error
	history       []ChangelogEntry
	historyErr    error
	timeline      []ChangelogEntry
	timelineErr   error
}

func (s *stubRepo) Search(_ context.Context, _ SearchRequest) ([]SearchResult, error) {
	return s.searchResults, s.searchErr
}
func (s *stubRepo) ListFlags(_ context.Context, _ string, _ FlagFilters) ([]FlagSummary, error) {
	return s.flags, s.flagsErr
}
func (s *stubRepo) GetFlag(_ context.Context, _, _ string) (FlagDetail, error) {
	return s.flagDetail, s.flagDetailErr
}
func (s *stubRepo) GetFlagHistory(_ context.Context, _, _ string, _ Pagination) ([]ChangelogEntry, error) {
	return s.history, s.historyErr
}
func (s *stubRepo) GetTimeline(_ context.Context, _ string, _ Pagination) ([]ChangelogEntry, error) {
	return s.timeline, s.timelineErr
}

// testClient has an explicit timeout so tests fail fast rather than hanging.
var testClient = &http.Client{Timeout: 5 * time.Second}

func newTestServer(t *testing.T, repo SearchRepository) *httptest.Server {
	t.Helper()
	logger := zerolog.New(io.Discard)
	testApp := newApp(loadConfig(), logger, repo)
	return httptest.NewServer(testApp.router())
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func getWithOrgID(t *testing.T, url, orgID string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Org-Id", orgID)
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

var repoErr = errors.New("repository unavailable")

// --- Middleware ---

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
	resp := getWithOrgID(t, srv.URL+"/search?q=checkout", defaultOrgID)
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
	resp := get(t, srv.URL+"/search?q=test")
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
	resp := getWithOrgID(t, srv.URL+"/flags", defaultOrgID)
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
	resp := get(t, srv.URL+"/flags")
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
	resp := getWithOrgID(t, srv.URL+"/flags/checkout-redesign", defaultOrgID)
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
	resp := get(t, srv.URL+"/flags/does-not-exist")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetFlagDetailRepositoryError(t *testing.T) {
	srv := newTestServer(t, &stubRepo{flagDetailErr: repoErr})
	defer srv.Close()
	resp := get(t, srv.URL+"/flags/any-flag")
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
	resp := getWithOrgID(t, srv.URL+"/flags/checkout-redesign/history", defaultOrgID)
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
	resp := get(t, srv.URL+"/flags/checkout-redesign/history?page=0")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGetFlagHistoryRepositoryError(t *testing.T) {
	srv := newTestServer(t, &stubRepo{historyErr: repoErr})
	defer srv.Close()
	resp := get(t, srv.URL+"/flags/any-flag/history")
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
	resp := getWithOrgID(t, srv.URL+"/timeline", defaultOrgID)
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
	resp := get(t, srv.URL+"/timeline")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

// --- Unit tests for pure functions ---

func TestParseLimit(t *testing.T) {
	cases := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"", 10, false},
		{"5", 5, false},
		{"100", 100, false},
		{"1", 1, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"101", 0, true},
		{"abc", 0, true},
		{"  ", 10, false},
	}
	for _, tc := range cases {
		got, err := parseLimit(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseLimit(%q): wantErr=%v, got err=%v", tc.input, tc.wantErr, err)
		}
		if err == nil && got != tc.want {
			t.Errorf("parseLimit(%q): want %d, got %d", tc.input, tc.want, got)
		}
	}
}

func TestParsePage(t *testing.T) {
	cases := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"", 1, false},
		{"1", 1, false},
		{"5", 5, false},
		{"100", 100, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
		{"  ", 1, false},
	}
	for _, tc := range cases {
		got, err := parsePage(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("parsePage(%q): wantErr=%v, got err=%v", tc.input, tc.wantErr, err)
		}
		if err == nil && got != tc.want {
			t.Errorf("parsePage(%q): want %d, got %d", tc.input, tc.want, got)
		}
	}
}

// TODO(Phase 2B): add integration tests for PostgresSearchRepository using
// testcontainers-go. Tests must cover: Search, ListFlags, GetFlag,
// GetFlagHistory, GetTimeline, org_id scoping (org A cannot see org B data),
// pagination, and 404 for unknown flag key.
