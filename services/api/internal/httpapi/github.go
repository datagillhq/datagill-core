package httpapi

import (
	"net/http"
	"net/url"

	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/services/api/internal/githubapp"
)

type startGitHubInstallRequest struct {
	ReturnTo string `json:"return_to"`
}

// startGitHubInstall serves POST /v1/auth/github: it returns the URL that starts the
// GitHub App install.
func (s *server) startGitHubInstall(w http.ResponseWriter, r *http.Request) {
	var req startGitHubInstallRequest
	if err := decodeBody(r, &req, true); err != nil {
		writeCode(w, http.StatusBadRequest, codeInvalidBody)
		return
	}

	p := principalFrom(r.Context())
	install, err := s.GitHub.Start(p.OrgID, p.UserID, req.ReturnTo)
	if err != nil {
		writeInternal(w, r, err, "start github install failed")
		return
	}
	writeJSON(w, http.StatusOK, install)
}

// completeGitHubInstall serves GET /v1/auth/github, where GitHub sends the browser after
// the install. It always redirects back to the UI, with the outcome in the query string.
func (s *server) completeGitHubInstall(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	returnTo, err := s.GitHub.Complete(r.Context(), githubapp.Callback{
		State:          q.Get("state"),
		Code:           q.Get("code"),
		InstallationID: q.Get("installation_id"),
	})

	outcome := url.Values{"provider": {"github"}, "status": {"connected"}}
	if err != nil {
		reason := githubapp.FailureReason(err)
		zerolog.Ctx(r.Context()).Warn().Err(err).Str("reason", reason).Msg("github install callback failed")
		outcome.Set("status", "error")
		outcome.Set("code", reason)
	}
	http.Redirect(w, r, uiURL(s.FrontendURL, returnTo, outcome), http.StatusFound)
}

// uiURL resolves path against the UI's base URL and adds query. It falls back to the
// default integrations page if either can't be parsed.
func uiURL(frontendURL, path string, query url.Values) string {
	target, err := url.Parse(frontendURL)
	if err == nil {
		target, err = target.Parse(githubapp.SafeReturnTo(path))
	}
	if err != nil {
		target = &url.URL{Path: githubapp.DefaultReturnTo}
	}

	merged := target.Query()
	for k, v := range query {
		merged[k] = v
	}
	target.RawQuery = merged.Encode()
	return target.String()
}
