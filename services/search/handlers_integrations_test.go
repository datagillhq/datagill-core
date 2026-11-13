package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
	"github.com/datagill/datagill-core/services/search/analytics"
)

// requestWithToken sends a method+body request with a JWT for testOrgID/testUserID.
func requestWithToken(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+makeToken(testOrgID, testUserID))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestProvidersHandler(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := getWithToken(t, srv.URL+"/v1/integrations/providers", testOrgID, testUserID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCreateIntegrationHandler(t *testing.T) {
	body := createIntegrationRequest{
		Provider: models.SourceLaunchDarkly,
		APIKey:   "api-key",
		Config:   models.IntegrationConfig{EnvironmentKey: "production"},
	}

	t.Run("success", func(t *testing.T) {
		srv := newTestServerFull(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/integrations", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
	})

	t.Run("schedule failure rolls back the integration", func(t *testing.T) {
		integrationsRepo := &stubIntegrationsRepo{}
		scheduler := &stubScheduler{createErr: errors.New("temporal unavailable")}
		srv := newTestServerWithGitHubAuthAndScheduler(t, integrationsRepo, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry(), scheduler, nil)
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/integrations", body)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", resp.StatusCode)
		}
		if scheduler.createCall == nil {
			t.Fatal("expected a schedule create attempt")
		}
		if len(integrationsRepo.deletedKeys) != 1 || integrationsRepo.deletedKeys[0] != scheduler.createCall.Key {
			t.Errorf("expected the new integration %s to be soft-deleted, got %v", scheduler.createCall.Key, integrationsRepo.deletedKeys)
		}
	})

	t.Run("invalid api key", func(t *testing.T) {
		srv := newTestServerFull(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{err: ErrInvalidAPIKey}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/integrations", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("provider unreachable", func(t *testing.T) {
		srv := newTestServerFull(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{err: ErrProviderUnreachable}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/integrations", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("duplicate provider", func(t *testing.T) {
		existing := &stubIntegrationsRepo{integrations: []models.Integration{
			{Source: models.SourceLaunchDarkly, State: models.IntegrationStateActive},
		}}
		srv := newTestServerFull(t, existing, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/integrations", body)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409, got %d", resp.StatusCode)
		}
	})

	t.Run("github must use auth flow", func(t *testing.T) {
		srv := newTestServerFull(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/integrations", createIntegrationRequest{
			Provider: models.SourceGitHub,
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("no auth", func(t *testing.T) {
		srv := newTestServer(t)
		defer srv.Close()

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/v1/integrations", nil)
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})
}

func TestListIntegrationsHandler(t *testing.T) {
	integrationsRepo := &stubIntegrationsRepo{integrations: []models.Integration{
		{Key: uuid.New(), Source: models.SourceLaunchDarkly, State: models.IntegrationStateActive},
	}}
	srv := newTestServerFull(t, integrationsRepo, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
	defer srv.Close()

	resp := getWithToken(t, srv.URL+"/v1/integrations", testOrgID, testUserID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestStartGitHubAuthHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		githubAuth := &stubGitHubAuth{
			startResp: githubStartResponse{
				InstallURL: "https://github.com/apps/datagill/installations/new?state=signed",
				ExpiresAt:  time.Date(2026, 4, 28, 12, 15, 0, 0, time.UTC),
			},
		}
		srv := newTestServerWithGitHubAuth(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry(), githubAuth)
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/auth/github", map[string]any{"return_to": "/settings/integrations"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		if githubAuth.startCall == nil {
			t.Fatal("expected Start to be called")
		}
		if githubAuth.startCall.orgID != uuid.MustParse(testOrgID) {
			t.Fatalf("expected org_id %s, got %s", testOrgID, githubAuth.startCall.orgID)
		}
		if githubAuth.startCall.userID != testUserID {
			t.Fatalf("expected user_id %q, got %q", testUserID, githubAuth.startCall.userID)
		}
		if githubAuth.startCall.returnTo != "/settings/integrations" {
			t.Fatalf("expected return_to to be forwarded, got %q", githubAuth.startCall.returnTo)
		}
	})

	t.Run("empty body is allowed", func(t *testing.T) {
		githubAuth := &stubGitHubAuth{
			startResp: githubStartResponse{
				InstallURL: "https://github.com/apps/datagill/installations/new?state=signed",
				ExpiresAt:  time.Date(2026, 4, 28, 12, 15, 0, 0, time.UTC),
			},
		}
		srv := newTestServerWithGitHubAuth(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry(), githubAuth)
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/v1/auth/github", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+makeToken(testOrgID, testUserID))
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		if githubAuth.startCall == nil {
			t.Fatal("expected Start to be called")
		}
		if githubAuth.startCall.returnTo != "" {
			t.Fatalf("expected empty return_to, got %q", githubAuth.startCall.returnTo)
		}
	})

	t.Run("invalid body", func(t *testing.T) {
		srv := newTestServerWithGitHubAuth(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry(), &stubGitHubAuth{})
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/v1/auth/github", strings.NewReader("{"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+makeToken(testOrgID, testUserID))
		req.Header.Set("Content-Type", "application/json")
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("service error", func(t *testing.T) {
		srv := newTestServerWithGitHubAuth(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry(), &stubGitHubAuth{startErr: context.DeadlineExceeded})
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPost, srv.URL+"/v1/auth/github", map[string]any{"return_to": "/settings/integrations"})
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", resp.StatusCode)
		}
	})

	t.Run("no auth", func(t *testing.T) {
		srv := newTestServer(t)
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/v1/auth/github", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})
}

func TestGitHubAuthCallbackHandler(t *testing.T) {
	noRedirectClient := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	t.Run("success redirect", func(t *testing.T) {
		githubAuth := &stubGitHubAuth{
			completeRedirect: "http://localhost:3001/settings/integrations?provider=github&status=connected",
		}
		srv := newTestServerWithGitHubAuth(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry(), githubAuth)
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/auth/github?state=signed&code=oauth-code&installation_id=12345", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("expected 302, got %d", resp.StatusCode)
		}
		if got := resp.Header.Get("Location"); got != githubAuth.completeRedirect {
			t.Fatalf("expected redirect %q, got %q", githubAuth.completeRedirect, got)
		}
		if githubAuth.completeQuery.Get("state") != "signed" || githubAuth.completeQuery.Get("code") != "oauth-code" {
			t.Fatalf("expected query to be forwarded, got %v", githubAuth.completeQuery)
		}
	})

	t.Run("error redirect from service", func(t *testing.T) {
		githubAuth := &stubGitHubAuth{
			completeRedirect: "http://localhost:3001/settings/integrations?provider=github&status=error&code=invalid_state",
			completeErr:      context.DeadlineExceeded,
		}
		srv := newTestServerWithGitHubAuth(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry(), githubAuth)
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/auth/github?state=signed", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("expected 302, got %d", resp.StatusCode)
		}
		if got := resp.Header.Get("Location"); got != githubAuth.completeRedirect {
			t.Fatalf("expected redirect %q, got %q", githubAuth.completeRedirect, got)
		}
	})

	t.Run("fallback redirect when service returns empty redirect", func(t *testing.T) {
		srv := newTestServerWithGitHubAuth(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry(), &stubGitHubAuth{completeErr: context.DeadlineExceeded})
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/auth/github?state=signed", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("expected 302, got %d", resp.StatusCode)
		}
		target, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if target.Query().Get("code") != "integration_create_failed" {
			t.Fatalf("expected fallback code integration_create_failed, got %q", target.Query().Get("code"))
		}
	})
}

func TestUpdateIntegrationHandler(t *testing.T) {
	key := uuid.New()
	newState := models.IntegrationStatePaused

	t.Run("success", func(t *testing.T) {
		integrationsRepo := &stubIntegrationsRepo{
			updateResult: models.Integration{Key: key, Source: models.SourceLaunchDarkly, State: newState},
		}
		srv := newTestServerFull(t, integrationsRepo, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPut, srv.URL+"/v1/integrations/"+key.String(), map[string]any{"state": "paused"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("not found", func(t *testing.T) {
		integrationsRepo := &stubIntegrationsRepo{updateErr: repository.ErrNotFound}
		srv := newTestServerFull(t, integrationsRepo, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPut, srv.URL+"/v1/integrations/"+key.String(), map[string]any{"state": "paused"})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("invalid api key on update", func(t *testing.T) {
		srv := newTestServerFull(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{err: ErrInvalidAPIKey}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodPut, srv.URL+"/v1/integrations/"+key.String(), map[string]any{"api_key": "bad-key"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}

func TestDeleteIntegrationHandler(t *testing.T) {
	key := uuid.New()

	t.Run("success", func(t *testing.T) {
		integrationsRepo := &stubIntegrationsRepo{
			deleteResult: models.Integration{Key: key, State: models.IntegrationStateDeleted},
		}
		srv := newTestServerFull(t, integrationsRepo, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodDelete, srv.URL+"/v1/integrations/"+key.String(), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("not found", func(t *testing.T) {
		integrationsRepo := &stubIntegrationsRepo{deleteErr: repository.ErrNotFound}
		srv := newTestServerFull(t, integrationsRepo, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodDelete, srv.URL+"/v1/integrations/"+key.String(), nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("wrong org returns 404 not 403", func(t *testing.T) {
		integrationsRepo := &stubIntegrationsRepo{deleteErr: repository.ErrNotFound}
		srv := newTestServerFull(t, integrationsRepo, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
		defer srv.Close()

		resp := requestWithToken(t, http.MethodDelete, srv.URL+"/v1/integrations/"+key.String(), nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 (not 403) for wrong org, got %d", resp.StatusCode)
		}
	})
}
