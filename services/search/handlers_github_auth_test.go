package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/datagill/datagill-core/services/search/analytics"
)

func TestGitHubAuthStartHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := newTestServerFull(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/auth/github", map[string]any{
			"return_to": "/settings/integrations",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		var body githubAuthStartResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.InstallURL == "" {
			t.Fatal("expected install_url to be set")
		}
		if !strings.Contains(body.InstallURL, "github.com/apps/datagill-test/installations/new") {
			t.Fatalf("unexpected install_url %q", body.InstallURL)
		}
		parsed, err := url.Parse(body.InstallURL)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Query().Get("state") == "" {
			t.Fatal("expected signed state in install_url")
		}
	})

	t.Run("requires jwt", func(t *testing.T) {
		srv := newTestServer(t)
		defer srv.Close()

		resp := requestWithoutToken(t, http.MethodPost, srv.URL+"/v1/auth/github", `{"return_to":"/settings/integrations"}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})
}

func TestGitHubAuthCallbackHandler(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/auth/github", map[string]any{
		"return_to": "/settings/integrations",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from start, got %d", resp.StatusCode)
	}

	var start githubAuthStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&start); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(start.InstallURL)
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("expected state in install_url")
	}

	callbackURL := srv.URL + "/v1/auth/github?code=abc123&state=" + url.QueryEscape(state) + "&installation_id=42&setup_action=install"
	callbackResp := get(t, callbackURL)
	if callbackResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", callbackResp.StatusCode)
	}

	var body githubAuthCallbackResponse
	if err := json.NewDecoder(callbackResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "abc123" {
		t.Fatalf("expected code abc123, got %q", body.Code)
	}
	if body.InstallationID != "42" {
		t.Fatalf("expected installation_id 42, got %q", body.InstallationID)
	}
	if body.SetupAction != "install" {
		t.Fatalf("expected setup_action install, got %q", body.SetupAction)
	}
	if body.DecodedState == nil || body.DecodedState.OrgID != testOrgID || body.DecodedState.UserID != testUserID {
		t.Fatalf("unexpected decoded state: %+v", body.DecodedState)
	}
}

func TestGitHubAuthCallbackHandlerRejectsInvalidState(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := get(t, srv.URL+"/v1/auth/github?code=abc123&state=bad-state")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func requestWithoutToken(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
