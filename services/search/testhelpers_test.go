package main

import (
	"context"
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

// repoErr is a generic repository error for error-path tests.
var repoErr = errors.New("repository unavailable")

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
