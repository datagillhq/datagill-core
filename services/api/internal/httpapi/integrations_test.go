package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/services/api/internal/integrations"
)

func TestListProviders(t *testing.T) {
	srv := newTestServer(t, testDeps())
	resp := do(t, http.MethodGet, srv.URL+"/v1/integrations/providers", validToken(t), nil)
	assertStatus(t, resp, http.StatusOK)

	providers, _ := decodeJSON(t, resp)["providers"].([]any)
	if len(providers) != 2 || providers[0] != "launchdarkly" || providers[1] != "github" {
		t.Fatalf("unexpected providers: %v", providers)
	}
}

func TestListIntegrations(t *testing.T) {
	deps := testDeps()
	stub := &stubIntegrations{list: []models.Integration{
		{Key: uuid.New(), Source: models.SourceLaunchDarkly, State: models.IntegrationStateActive, APIKey: "secret"},
	}}
	deps.Integrations = stub
	srv := newTestServer(t, deps)

	resp := do(t, http.MethodGet, srv.URL+"/v1/integrations", validToken(t), nil)
	assertStatus(t, resp, http.StatusOK)

	list, _ := decodeJSON(t, resp)["integrations"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 integration, got %v", list)
	}
	if _, leaked := list[0].(map[string]any)["api_key"]; leaked {
		t.Fatal("API key must never be returned")
	}
	if stub.orgID != uuid.MustParse(testOrgID) {
		t.Fatalf("expected org from token, got %s", stub.orgID)
	}
}

func TestCreateIntegration(t *testing.T) {
	body := map[string]any{"source": "launchdarkly", "api_key": "api-key", "config": map[string]any{"environment_key": "production"}}

	t.Run("success passes the request through", func(t *testing.T) {
		deps := testDeps()
		stub := &stubIntegrations{result: models.Integration{Key: uuid.New(), Source: models.SourceLaunchDarkly}}
		deps.Integrations = stub
		srv := newTestServer(t, deps)

		assertStatus(t, do(t, http.MethodPost, srv.URL+"/v1/integrations", validToken(t), body), http.StatusCreated)
		if stub.createInput == nil || stub.createInput.Source != models.SourceLaunchDarkly || stub.createInput.APIKey != "api-key" {
			t.Fatalf("unexpected create input: %+v", stub.createInput)
		}
		if stub.orgID != uuid.MustParse(testOrgID) {
			t.Fatalf("expected org from token, got %s", stub.orgID)
		}
	})

	t.Run("invalid body", func(t *testing.T) {
		srv := newTestServer(t, testDeps())
		assertCode(t, do(t, http.MethodPost, srv.URL+"/v1/integrations", validToken(t), "{"), http.StatusBadRequest, codeInvalidBody)
	})

	t.Run("no auth", func(t *testing.T) {
		srv := newTestServer(t, testDeps())
		assertStatus(t, do(t, http.MethodPost, srv.URL+"/v1/integrations", "", body), http.StatusUnauthorized)
	})
}

// Every service error maps to one status and code; anything unknown is a 500.
func TestIntegrationErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{integrations.ErrNotFound, http.StatusNotFound, codeNotFound},
		{integrations.ErrDuplicateProvider, http.StatusConflict, codeDuplicateProvider},
		{integrations.ErrUseGitHubApp, http.StatusBadRequest, codeUseGitHubAuth},
		{integrations.ErrInvalidAPIKey, http.StatusBadRequest, codeInvalidAPIKey},
		{integrations.ErrProviderUnreachable, http.StatusBadRequest, codeUnreachable},
		{integrations.ErrInvalidState, http.StatusBadRequest, codeInvalidState},
		{errors.New("db down"), http.StatusInternalServerError, codeInternal},
	}
	key := uuid.NewString()
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			deps := testDeps()
			deps.Integrations = &stubIntegrations{err: tt.err}
			srv := newTestServer(t, deps)

			assertCode(t, do(t, http.MethodPost, srv.URL+"/v1/integrations", validToken(t), map[string]any{"source": "launchdarkly"}), tt.status, tt.code)
			assertCode(t, do(t, http.MethodPut, srv.URL+"/v1/integrations/"+key, validToken(t), map[string]any{"state": "paused"}), tt.status, tt.code)
			assertCode(t, do(t, http.MethodDelete, srv.URL+"/v1/integrations/"+key, validToken(t), nil), tt.status, tt.code)
		})
	}
}

func TestUpdateIntegration(t *testing.T) {
	key := uuid.New()

	t.Run("success passes the patch through", func(t *testing.T) {
		deps := testDeps()
		stub := &stubIntegrations{result: models.Integration{Key: key, State: models.IntegrationStatePaused}}
		deps.Integrations = stub
		srv := newTestServer(t, deps)

		resp := do(t, http.MethodPut, srv.URL+"/v1/integrations/"+key.String(), validToken(t), map[string]any{"state": "paused"})
		assertStatus(t, resp, http.StatusOK)
		if stub.patch == nil || stub.patch.State == nil || *stub.patch.State != models.IntegrationStatePaused || stub.patch.APIKey != nil {
			t.Fatalf("unexpected patch: %+v", stub.patch)
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		srv := newTestServer(t, testDeps())
		assertCode(t, do(t, http.MethodPut, srv.URL+"/v1/integrations/not-a-uuid", validToken(t), map[string]any{}), http.StatusBadRequest, codeInvalidKey)
	})

	t.Run("invalid body", func(t *testing.T) {
		srv := newTestServer(t, testDeps())
		assertCode(t, do(t, http.MethodPut, srv.URL+"/v1/integrations/"+key.String(), validToken(t), "{"), http.StatusBadRequest, codeInvalidBody)
	})
}

func TestDeleteIntegration(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		deps := testDeps()
		deps.Integrations = &stubIntegrations{result: models.Integration{State: models.IntegrationStateDeleted}}
		srv := newTestServer(t, deps)
		assertStatus(t, do(t, http.MethodDelete, srv.URL+"/v1/integrations/"+uuid.NewString(), validToken(t), nil), http.StatusOK)
	})

	t.Run("invalid key", func(t *testing.T) {
		srv := newTestServer(t, testDeps())
		assertCode(t, do(t, http.MethodDelete, srv.URL+"/v1/integrations/not-a-uuid", validToken(t), nil), http.StatusBadRequest, codeInvalidKey)
	})
}
