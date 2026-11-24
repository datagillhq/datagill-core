package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/services/api/internal/config"
)

var (
	testOrgID  = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	testUserID = "00000000-0000-0000-0000-000000000002"
	testNow    = time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
)

type stubConnector struct {
	err    error
	orgID  uuid.UUID
	config *models.IntegrationConfig
}

func (s *stubConnector) ConnectGitHub(_ context.Context, orgID uuid.UUID, cfg models.IntegrationConfig) (models.Integration, error) {
	s.orgID = orgID
	s.config = &cfg
	return models.Integration{Key: uuid.New(), OrgID: orgID, Source: models.SourceGitHub}, s.err
}

// newTestFlow returns a fully configured Flow whose GitHub calls go to api (may be nil).
func newTestFlow(t *testing.T, connector Connector, api *httptest.Server) *Flow {
	t.Helper()
	f, err := New(config.GitHubApp{
		AppID:        "123456",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		PrivateKey:   testPrivateKeyPEM(t),
		Slug:         "datagill",
		CallbackURL:  "http://localhost:8082/v1/auth/github",
		StateSecret:  "state-secret",
	}, connector, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.now = func() time.Time { return testNow }
	f.github.now = f.now
	if api != nil {
		f.github.oauthURL = api.URL + "/login/oauth/access_token"
		f.github.apiBaseURL = api.URL
	}
	return f
}

// fakeGitHub serves the three endpoints the callback calls. userInstallations is the
// JSON body for GET /user/installations, by page.
func fakeGitHub(t *testing.T, userInstallations func(page string) string) *httptest.Server {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"access_token":"user-token"}`))
		case "/user/installations":
			if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
				t.Errorf("expected user token auth, got %q", got)
			}
			_, _ = w.Write([]byte(userInstallations(r.URL.Query().Get("page"))))
		case "/app/installations/12345":
			if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ey") {
				t.Errorf("expected app JWT auth, got %q", got)
			}
			_, _ = w.Write([]byte(`{"id":12345,"account":{"login":"acme-org","type":"Organization"},"repository_selection":"selected"}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(api.Close)
	return api
}

func visibleInstallation(string) string { return `{"installations":[{"id":12345}]}` }

func validCallback(t *testing.T, f *Flow, returnTo string) Callback {
	t.Helper()
	token, err := f.state.encode(state{
		OrgID:     testOrgID,
		UserID:    testUserID,
		Nonce:     "nonce",
		ReturnTo:  returnTo,
		IssuedAt:  testNow,
		ExpiresAt: testNow.Add(stateTTL),
	})
	if err != nil {
		t.Fatalf("encode state: %v", err)
	}
	return Callback{State: token, Code: "oauth-code", InstallationID: "12345"}
}

func TestStart(t *testing.T) {
	f := newTestFlow(t, &stubConnector{}, nil)

	install, err := f.Start(testOrgID, testUserID, "/settings/integrations")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if install.ExpiresAt != testNow.Add(stateTTL) {
		t.Fatalf("expected expires_at %s, got %s", testNow.Add(stateTTL), install.ExpiresAt)
	}

	u, err := url.Parse(install.URL)
	if err != nil {
		t.Fatalf("parse install url: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "https://github.com/apps/datagill/installations/new" {
		t.Fatalf("unexpected install url base: %s", got)
	}
	if got := u.Query().Get("request_oauth_on_install"); got != "true" {
		t.Fatalf("expected request_oauth_on_install=true, got %q", got)
	}

	st := decodeStatePayload(t, u.Query().Get("state"))
	if st.OrgID != testOrgID || st.UserID != testUserID || st.ReturnTo != "/settings/integrations" || st.Nonce == "" {
		t.Fatalf("unexpected state: %+v", st)
	}
}

func TestStart_RequiresConfig(t *testing.T) {
	f, err := New(config.GitHubApp{}, &stubConnector{}, &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.Start(testOrgID, testUserID, ""); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestNew_RejectsBadPrivateKey(t *testing.T) {
	if _, err := New(config.GitHubApp{PrivateKey: []byte("not a pem")}, &stubConnector{}, &http.Client{Timeout: time.Second}); err == nil {
		t.Fatal("expected error for unparsable private key")
	}
}

func TestSafeReturnTo(t *testing.T) {
	tests := map[string]string{
		"":                       DefaultReturnTo,
		"/settings/integrations": "/settings/integrations",
		"/dashboard?tab=flags":   "/dashboard?tab=flags",
		"https://evil.example":   DefaultReturnTo,
		"//evil.example":         DefaultReturnTo,
		`/\evil.example`:         DefaultReturnTo,
		"relative/path":          DefaultReturnTo,
	}
	for in, want := range tests {
		if got := SafeReturnTo(in); got != want {
			t.Errorf("SafeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComplete(t *testing.T) {
	t.Run("success connects the installation", func(t *testing.T) {
		connector := &stubConnector{}
		f := newTestFlow(t, connector, fakeGitHub(t, visibleInstallation))

		returnTo, err := f.Complete(t.Context(), validCallback(t, f, "/settings/integrations"))
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if returnTo != "/settings/integrations" {
			t.Fatalf("unexpected returnTo %q", returnTo)
		}
		want := models.IntegrationConfig{InstallationID: 12345, AccountLogin: "acme-org", AccountType: "Organization", RepositorySelection: "selected"}
		if connector.orgID != testOrgID || connector.config == nil || *connector.config != want {
			t.Fatalf("unexpected connect: org=%s config=%+v", connector.orgID, connector.config)
		}
	})

	t.Run("finds installation on second page", func(t *testing.T) {
		ids := make([]string, installationsPage)
		for i := range ids {
			ids[i] = fmt.Sprintf(`{"id":%d}`, i+1)
		}
		api := fakeGitHub(t, func(page string) string {
			if page == "2" {
				return visibleInstallation(page)
			}
			return `{"installations":[` + strings.Join(ids, ",") + `]}`
		})
		f := newTestFlow(t, &stubConnector{}, api)

		if _, err := f.Complete(t.Context(), validCallback(t, f, "/settings/integrations")); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	})

	t.Run("bad state fails with invalid_state and the default page", func(t *testing.T) {
		f := newTestFlow(t, &stubConnector{}, nil)
		for _, token := range []string{"", "bad.state", "nodot"} {
			returnTo, err := f.Complete(t.Context(), Callback{State: token, Code: "oauth-code"})
			assertFailure(t, err, ReasonInvalidState)
			if returnTo != DefaultReturnTo {
				t.Errorf("expected default return path, got %q", returnTo)
			}
		}
	})

	t.Run("expired state fails with invalid_state", func(t *testing.T) {
		f := newTestFlow(t, &stubConnector{}, nil)
		cb := validCallback(t, f, "/settings/integrations")
		f.now = func() time.Time { return testNow.Add(stateTTL + time.Second) }
		_, err := f.Complete(t.Context(), cb)
		assertFailure(t, err, ReasonInvalidState)
	})

	t.Run("installation not visible fails with github_auth_failed", func(t *testing.T) {
		api := fakeGitHub(t, func(string) string { return `{"installations":[]}` })
		f := newTestFlow(t, &stubConnector{}, api)

		returnTo, err := f.Complete(t.Context(), validCallback(t, f, "/onboarding"))
		assertFailure(t, err, ReasonAuthFailed)
		if returnTo != "/onboarding" {
			t.Errorf("expected return path from state, got %q", returnTo)
		}
	})

	t.Run("missing code fails with github_auth_failed", func(t *testing.T) {
		f := newTestFlow(t, &stubConnector{}, nil)
		cb := validCallback(t, f, "/settings/integrations")
		cb.Code = ""
		_, err := f.Complete(t.Context(), cb)
		assertFailure(t, err, ReasonAuthFailed)
	})

	t.Run("connect failure fails with integration_create_failed", func(t *testing.T) {
		temporalDown := errors.New("temporal unavailable")
		f := newTestFlow(t, &stubConnector{err: temporalDown}, fakeGitHub(t, visibleInstallation))

		_, err := f.Complete(t.Context(), validCallback(t, f, "/settings/integrations"))
		assertFailure(t, err, ReasonConnectFailed)
		if !errors.Is(err, temporalDown) {
			t.Fatalf("expected the connect error to be wrapped, got %v", err)
		}
	})
}

func TestFailureReason_DefaultsToConnectFailed(t *testing.T) {
	if got := FailureReason(errors.New("boom")); got != ReasonConnectFailed {
		t.Fatalf("expected %q, got %q", ReasonConnectFailed, got)
	}
}

func assertFailure(t *testing.T, err error, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s failure, got nil", reason)
	}
	if got := FailureReason(err); got != reason {
		t.Fatalf("expected reason %q, got %q (%v)", reason, got, err)
	}
}

func decodeStatePayload(t *testing.T, token string) state {
	t.Helper()
	payload, _, ok := strings.Cut(token, ".")
	if !ok {
		t.Fatalf("expected two-part token, got %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var st state
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	return st
}

func testPrivateKeyPEM(t *testing.T) []byte {
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
