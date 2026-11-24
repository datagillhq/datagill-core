package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/services/api/internal/githubapp"
)

func TestStartGitHubInstall(t *testing.T) {
	install := githubapp.Install{
		URL:       "https://github.com/apps/datagill/installations/new?state=signed",
		ExpiresAt: time.Date(2026, 4, 28, 12, 15, 0, 0, time.UTC),
	}

	t.Run("success", func(t *testing.T) {
		deps := testDeps()
		gh := &stubGitHub{install: install}
		deps.GitHub = gh
		srv := newTestServer(t, deps)

		resp := do(t, http.MethodPost, srv.URL+"/v1/auth/github", validToken(t), map[string]any{"return_to": "/settings/integrations"})
		assertStatus(t, resp, http.StatusOK)
		if body := decodeJSON(t, resp); body["install_url"] != install.URL {
			t.Fatalf("unexpected body: %v", body)
		}
		if gh.startOrgID != uuid.MustParse(testOrgID) || gh.startUserID != testUserID || gh.startReturn != "/settings/integrations" {
			t.Fatalf("unexpected Start call: org=%s user=%q returnTo=%q", gh.startOrgID, gh.startUserID, gh.startReturn)
		}
	})

	t.Run("empty body is allowed", func(t *testing.T) {
		deps := testDeps()
		gh := &stubGitHub{install: install}
		deps.GitHub = gh
		srv := newTestServer(t, deps)

		assertStatus(t, do(t, http.MethodPost, srv.URL+"/v1/auth/github", validToken(t), nil), http.StatusOK)
		if gh.startReturn != "" {
			t.Fatalf("expected empty return_to, got %q", gh.startReturn)
		}
	})

	t.Run("invalid body", func(t *testing.T) {
		srv := newTestServer(t, testDeps())
		assertCode(t, do(t, http.MethodPost, srv.URL+"/v1/auth/github", validToken(t), "{"), http.StatusBadRequest, codeInvalidBody)
	})

	t.Run("service error", func(t *testing.T) {
		deps := testDeps()
		deps.GitHub = &stubGitHub{startErr: githubapp.ErrNotConfigured}
		srv := newTestServer(t, deps)
		assertCode(t, do(t, http.MethodPost, srv.URL+"/v1/auth/github", validToken(t), nil), http.StatusInternalServerError, codeInternal)
	})

	t.Run("no auth", func(t *testing.T) {
		srv := newTestServer(t, testDeps())
		assertStatus(t, do(t, http.MethodPost, srv.URL+"/v1/auth/github", "", nil), http.StatusUnauthorized)
	})
}

func TestCompleteGitHubInstall(t *testing.T) {
	noRedirects := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	callback := func(t *testing.T, gh *stubGitHub) *url.URL {
		t.Helper()
		deps := testDeps()
		deps.GitHub = gh
		srv := newTestServer(t, deps)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/v1/auth/github?state=signed&code=oauth-code&installation_id=12345", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := noRedirects.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusFound)
		target, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		return target
	}

	t.Run("success redirects to the return path", func(t *testing.T) {
		gh := &stubGitHub{returnTo: "/onboarding?step=2"}
		target := callback(t, gh)

		if gh.callback != (githubapp.Callback{State: "signed", Code: "oauth-code", InstallationID: "12345"}) {
			t.Fatalf("callback params not forwarded: %+v", gh.callback)
		}
		assertRedirect(t, target, "/onboarding", "connected", "")
		if target.Query().Get("step") != "2" {
			t.Fatalf("expected existing query to be kept, got %s", target.RawQuery)
		}
	})

	for _, reason := range []string{githubapp.ReasonInvalidState, githubapp.ReasonAuthFailed, githubapp.ReasonConnectFailed} {
		t.Run("failure "+reason, func(t *testing.T) {
			err := &githubapp.CallbackError{Reason: reason, Err: errors.New("boom")}
			target := callback(t, &stubGitHub{returnTo: githubapp.DefaultReturnTo, completeErr: err})
			assertRedirect(t, target, githubapp.DefaultReturnTo, "error", reason)
		})
	}

	t.Run("unsafe return path falls back to the default page", func(t *testing.T) {
		target := callback(t, &stubGitHub{returnTo: "//evil.example"})
		if target.Host != "localhost:3001" {
			t.Fatalf("redirected off-site: %s", target)
		}
		assertRedirect(t, target, githubapp.DefaultReturnTo, "connected", "")
	})
}

// A failed GitHub connect reaches the browser only as an error code in a redirect, so the
// cause has to be logged or it is lost.
func TestGitHubInstall_LogsFailures(t *testing.T) {
	var logs bytes.Buffer
	deps := testDeps()
	deps.Logger = zerolog.New(&logs)
	deps.GitHub = &stubGitHub{
		startErr:    errors.New("github app is not configured"),
		returnTo:    githubapp.DefaultReturnTo,
		completeErr: &githubapp.CallbackError{Reason: githubapp.ReasonConnectFailed, Err: errors.New("temporal unavailable")},
	}
	srv := newTestServer(t, deps)

	do(t, http.MethodPost, srv.URL+"/v1/auth/github", validToken(t), nil)
	noRedirects := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/v1/auth/github?state=s&code=c&installation_id=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := noRedirects.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	for _, want := range []string{"github app is not configured", "temporal unavailable"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("expected %q to be logged, got:\n%s", want, logs.String())
		}
	}
}

func assertRedirect(t *testing.T, target *url.URL, path, status, code string) {
	t.Helper()
	if got := fmt.Sprintf("%s://%s%s", target.Scheme, target.Host, target.Path); got != testFrontendURL+path {
		t.Fatalf("expected redirect to %s, got %s", testFrontendURL+path, got)
	}
	q := target.Query()
	if q.Get("provider") != "github" || q.Get("status") != status || q.Get("code") != code {
		t.Fatalf("unexpected redirect query: %s", target.RawQuery)
	}
}
