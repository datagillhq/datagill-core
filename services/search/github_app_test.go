package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
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
}

func TestGitHubAuthComplete(t *testing.T) {
	privateKeyPEM := testGitHubPrivateKeyPEM(t)
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	orgID := uuid.MustParse(testOrgID)

	t.Run("success creates integration and schedule", func(t *testing.T) {
		var appAuthHeader string
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login/oauth/access_token":
				if r.Method != http.MethodPost {
					t.Fatalf("expected POST, got %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"user-token"}`))
			case "/user/installations":
				if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
					t.Fatalf("expected user token auth, got %q", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"installations":[{"id":12345}]}`))
			case "/app/installations/12345":
				appAuthHeader = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":12345,"account":{"login":"acme-org","type":"Organization"},"repository_selection":"selected"}`))
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer api.Close()

		repo := &stubIntegrationsRepo{
			upsertResult:  models.Integration{Key: uuid.New(), OrgID: orgID, Source: models.SourceGitHub},
			upsertCreated: true,
		}
		scheduler := &stubScheduler{}
		svc := newCallbackTestService(t, callbackTestConfig(privateKeyPEM), repo, &stubOrgRepo{}, scheduler, now, api.URL+"/login/oauth/access_token", api.URL)

		redirectURL, err := svc.Complete(t.Context(), callbackQuery(t, svc, orgID, "/settings/integrations"))
		if err != nil {
			t.Fatalf("Complete returned error: %v", err)
		}
		if redirectURL != "http://localhost:3001/settings/integrations?provider=github&status=connected" {
			t.Fatalf("unexpected redirect: %s", redirectURL)
		}
		if repo.upsertOrgID == nil || *repo.upsertOrgID != orgID {
			t.Fatalf("expected upsert org_id %s, got %v", orgID, repo.upsertOrgID)
		}
		if repo.upsertConfig == nil {
			t.Fatal("expected GitHub upsert config")
		}
		if repo.upsertConfig.InstallationID != 12345 || repo.upsertConfig.AccountLogin != "acme-org" || repo.upsertConfig.AccountType != "Organization" || repo.upsertConfig.RepositorySelection != "selected" {
			t.Fatalf("unexpected upsert config: %+v", *repo.upsertConfig)
		}
		if scheduler.createCall == nil {
			t.Fatal("expected schedule create call")
		}
		if !strings.HasPrefix(appAuthHeader, "Bearer ") {
			t.Fatalf("expected app JWT auth header, got %q", appAuthHeader)
		}
	})

	t.Run("duplicate installation updates without schedule create", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login/oauth/access_token":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"user-token"}`))
			case "/user/installations":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"installations":[{"id":12345}]}`))
			case "/app/installations/12345":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":12345,"account":{"login":"acme-org","type":"Organization"},"repository_selection":"all"}`))
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer api.Close()

		repo := &stubIntegrationsRepo{
			upsertResult: models.Integration{Key: uuid.New(), OrgID: orgID, Source: models.SourceGitHub},
		}
		scheduler := &stubScheduler{}
		svc := newCallbackTestService(t, callbackTestConfig(privateKeyPEM), repo, &stubOrgRepo{}, scheduler, now, api.URL+"/login/oauth/access_token", api.URL)

		redirectURL, err := svc.Complete(t.Context(), callbackQuery(t, svc, orgID, "/settings/integrations"))
		if err != nil {
			t.Fatalf("Complete returned error: %v", err)
		}
		if redirectURL != "http://localhost:3001/settings/integrations?provider=github&status=connected" {
			t.Fatalf("unexpected redirect: %s", redirectURL)
		}
		if scheduler.createCall != nil {
			t.Fatal("did not expect schedule create for existing integration")
		}
	})

	t.Run("invalid state redirects with invalid_state", func(t *testing.T) {
		svc := newCallbackTestService(t, callbackTestConfig(privateKeyPEM), &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubScheduler{}, now, "", "")

		redirectURL, err := svc.Complete(t.Context(), url.Values{
			"state": []string{"bad.state"},
			"code":  []string{"oauth-code"},
		})
		if err == nil {
			t.Fatal("expected invalid state error")
		}
		assertGitHubRedirect(t, redirectURL, "error", "invalid_state")
	})

	t.Run("installation not visible redirects with github_auth_failed", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login/oauth/access_token":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"user-token"}`))
			case "/user/installations":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"installations":[]}`))
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer api.Close()

		svc := newCallbackTestService(t, callbackTestConfig(privateKeyPEM), &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubScheduler{}, now, api.URL+"/login/oauth/access_token", api.URL)

		redirectURL, err := svc.Complete(t.Context(), callbackQuery(t, svc, orgID, "/settings/integrations"))
		if err == nil {
			t.Fatal("expected installation visibility error")
		}
		assertGitHubRedirect(t, redirectURL, "error", "github_auth_failed")
	})
}

func callbackTestConfig(privateKeyPEM []byte) config {
	return config{
		GitHubAppID:           "123456",
		GitHubAppClientID:     "client-id",
		GitHubAppClientSecret: "client-secret",
		GitHubAppPrivateKey:   privateKeyPEM,
		GitHubAppCallbackURL:  "http://localhost:8082/v1/auth/github",
		GitHubAppStateSecret:  "state-secret",
		FrontendURL:           "http://localhost:3001",
	}
}

func newCallbackTestService(t *testing.T, cfg config, repo IntegrationsRepository, orgRepo OrgRepository, scheduler Scheduler, now time.Time, oauthURL, apiBaseURL string) *githubAuthService {
	t.Helper()
	signer, err := parseGitHubPrivateKey(cfg.GitHubAppPrivateKey)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	return &githubAuthService{
		cfg:        cfg,
		repo:       repo,
		orgRepo:    orgRepo,
		scheduler:  scheduler,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		now:        func() time.Time { return now },
		signer:     signer,
		oauthURL:   oauthURL,
		apiBaseURL: apiBaseURL,
	}
}

func callbackQuery(t *testing.T, svc *githubAuthService, orgID uuid.UUID, returnTo string) url.Values {
	t.Helper()
	state, _, err := svc.signState(githubState{
		OrgID:     orgID,
		UserID:    testUserID,
		Nonce:     "nonce",
		ReturnTo:  returnTo,
		IssuedAt:  svc.now().UTC(),
		ExpiresAt: svc.now().UTC().Add(githubStateTTL),
	})
	if err != nil {
		t.Fatalf("sign state: %v", err)
	}
	return url.Values{
		"state":           []string{state},
		"code":            []string{"oauth-code"},
		"installation_id": []string{"12345"},
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

func testGitHubPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal rsa key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func assertGitHubRedirect(t *testing.T, rawURL, status, code string) {
	t.Helper()
	target, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if got := target.Scheme + "://" + target.Host + target.Path; got != "http://localhost:3001/settings/integrations" {
		t.Fatalf("unexpected redirect target: %s", got)
	}
	if got := target.Query().Get("provider"); got != "github" {
		t.Fatalf("expected provider=github, got %q", got)
	}
	if got := target.Query().Get("status"); got != status {
		t.Fatalf("expected status=%q, got %q", status, got)
	}
	if got := target.Query().Get("code"); got != code {
		t.Fatalf("expected code=%q, got %q", code, got)
	}
}
