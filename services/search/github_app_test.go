package main

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGitHubAuthStart(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	svc := &githubAuthService{
		cfg: config{
			GitHubAppSlug:        "datagill",
			GitHubAppStateSecret: "secret",
		},
		now: func() time.Time { return now },
	}

	resp, err := svc.Start(t.Context(), uuid.MustParse(testOrgID), testUserID, "/settings/integrations")
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if resp.ExpiresAt != now.Add(githubStateTTL) {
		t.Fatalf("expected expires_at %s, got %s", now.Add(githubStateTTL), resp.ExpiresAt)
	}

	installURL, err := url.Parse(resp.InstallURL)
	if err != nil {
		t.Fatalf("parse install url: %v", err)
	}
	if got := installURL.Scheme + "://" + installURL.Host + installURL.Path; got != "https://github.com/apps/datagill/installations/new" {
		t.Fatalf("unexpected install url base: %s", got)
	}
	if got := installURL.Query().Get("request_oauth_on_install"); got != "true" {
		t.Fatalf("expected request_oauth_on_install=true, got %q", got)
	}

	state := decodeGitHubState(t, installURL.Query().Get("state"))
	if state.OrgID != uuid.MustParse(testOrgID) {
		t.Fatalf("expected org_id %s, got %s", testOrgID, state.OrgID)
	}
	if state.UserID != testUserID {
		t.Fatalf("expected user_id %q, got %q", testUserID, state.UserID)
	}
	if state.ReturnTo != "/settings/integrations" {
		t.Fatalf("expected return_to /settings/integrations, got %q", state.ReturnTo)
	}
	if state.IssuedAt != now {
		t.Fatalf("expected issued_at %s, got %s", now, state.IssuedAt)
	}
	if state.ExpiresAt != now.Add(githubStateTTL) {
		t.Fatalf("expected state expires_at %s, got %s", now.Add(githubStateTTL), state.ExpiresAt)
	}
	if state.Nonce == "" {
		t.Fatal("expected nonce to be set")
	}
}

func TestGitHubAuthStartNormalizesReturnTo(t *testing.T) {
	svc := &githubAuthService{
		cfg: config{
			GitHubAppSlug:        "datagill",
			GitHubAppStateSecret: "secret",
		},
		now: func() time.Time { return time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC) },
	}

	resp, err := svc.Start(t.Context(), uuid.MustParse(testOrgID), testUserID, "https://evil.example")
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	installURL, err := url.Parse(resp.InstallURL)
	if err != nil {
		t.Fatalf("parse install url: %v", err)
	}
	state := decodeGitHubState(t, installURL.Query().Get("state"))
	if state.ReturnTo != "/settings/integrations" {
		t.Fatalf("expected normalized return_to, got %q", state.ReturnTo)
	}
}

func TestGitHubAuthStartRequiresConfig(t *testing.T) {
	svc := &githubAuthService{cfg: config{}}

	if _, err := svc.Start(t.Context(), uuid.MustParse(testOrgID), testUserID, ""); err == nil {
		t.Fatal("expected error when config is missing")
	}
}

func decodeGitHubState(t *testing.T, token string) githubState {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("expected two-part token, got %q", token)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	var state githubState
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	return state
}
