package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

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
