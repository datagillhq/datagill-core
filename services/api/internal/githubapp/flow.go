// Package githubapp runs the GitHub App install flow: it sends the user to GitHub with a
// signed state, then on the callback checks the user can see the installation and hands
// it to the integrations service.
package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/services/api/internal/config"
)

const (
	stateTTL      = 15 * time.Minute
	installURLFmt = "https://github.com/apps/%s/installations/new"

	// DefaultReturnTo is the UI page the callback returns to when none (or an unsafe one)
	// was given.
	DefaultReturnTo = "/settings/integrations"
)

// Reasons a callback failed. They are sent to the UI as the redirect's code parameter.
const (
	ReasonInvalidState  = "invalid_state"
	ReasonAuthFailed    = "github_auth_failed"
	ReasonConnectFailed = "integration_create_failed"
)

// ErrNotConfigured is returned when the GitHub App credentials needed for a step are unset.
var ErrNotConfigured = errors.New("github app is not configured")

// CallbackError is a failed callback, with the reason to show the user.
type CallbackError struct {
	Reason string
	Err    error
}

// Error implements error.
func (e *CallbackError) Error() string { return e.Reason + ": " + e.Err.Error() }

// Unwrap returns the underlying error.
func (e *CallbackError) Unwrap() error { return e.Err }

// FailureReason returns the user-facing reason for a Complete error. Errors that aren't a
// CallbackError are treated as a failure to save the integration.
func FailureReason(err error) string {
	var cbErr *CallbackError
	if errors.As(err, &cbErr) {
		return cbErr.Reason
	}
	return ReasonConnectFailed
}

// Connector saves a verified installation as the org's GitHub integration.
type Connector interface {
	ConnectGitHub(ctx context.Context, orgID uuid.UUID, config models.IntegrationConfig) (models.Integration, error)
}

// Flow runs the install flow.
type Flow struct {
	slug      string
	state     stateCodec
	github    *client
	connector Connector
	now       func() time.Time
}

// New returns a Flow. Missing credentials are allowed (the flow then fails with
// ErrNotConfigured), but a private key that is set and can't be parsed is an error.
func New(cfg config.GitHubApp, connector Connector, httpClient *http.Client) (*Flow, error) {
	var appKey *rsa.PrivateKey
	if len(cfg.PrivateKey) > 0 {
		key, err := parsePrivateKey(cfg.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("github app private key: %w", err)
		}
		appKey = key
	}
	return &Flow{
		slug:      cfg.Slug,
		state:     stateCodec{secret: []byte(cfg.StateSecret)},
		connector: connector,
		now:       time.Now,
		github: &client{
			http:         httpClient,
			oauthURL:     defaultOAuthURL,
			apiBaseURL:   defaultAPIBaseURL,
			appID:        cfg.AppID,
			clientID:     cfg.ClientID,
			clientSecret: cfg.ClientSecret,
			callbackURL:  cfg.CallbackURL,
			appKey:       appKey,
			now:          time.Now,
		},
	}, nil
}

// Install is where to send the user to install the app.
type Install struct {
	URL       string    `json:"install_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Start returns the GitHub install URL for the org, carrying a signed state that the
// callback verifies. returnTo is the UI path to come back to.
func (f *Flow) Start(orgID uuid.UUID, userID, returnTo string) (Install, error) {
	if f.slug == "" || len(f.state.secret) == 0 {
		return Install{}, ErrNotConfigured
	}

	now := f.now().UTC()
	token, err := f.state.encode(state{
		OrgID:     orgID,
		UserID:    userID,
		Nonce:     rand.Text(),
		ReturnTo:  SafeReturnTo(returnTo),
		IssuedAt:  now,
		ExpiresAt: now.Add(stateTTL),
	})
	if err != nil {
		return Install{}, err
	}

	installURL, err := url.Parse(fmt.Sprintf(installURLFmt, url.PathEscape(f.slug)))
	if err != nil {
		return Install{}, fmt.Errorf("build install url: %w", err)
	}
	installURL.RawQuery = url.Values{
		"state":                    {token},
		"request_oauth_on_install": {"true"},
	}.Encode()

	return Install{URL: installURL.String(), ExpiresAt: now.Add(stateTTL)}, nil
}

// Callback holds the query parameters GitHub sends to the callback URL.
type Callback struct {
	State          string
	Code           string
	InstallationID string
}

// Complete verifies the callback and connects the installation. It returns the UI path to
// send the user back to, even on failure; failures are a *CallbackError.
func (f *Flow) Complete(ctx context.Context, cb Callback) (returnTo string, err error) {
	if cb.State == "" {
		return DefaultReturnTo, &CallbackError{ReasonInvalidState, errors.New("missing state")}
	}
	st, err := f.state.decode(cb.State, f.now().UTC())
	if err != nil {
		return DefaultReturnTo, &CallbackError{ReasonInvalidState, err}
	}

	cfg, err := f.verifyInstallation(ctx, cb)
	if err != nil {
		return st.ReturnTo, &CallbackError{ReasonAuthFailed, err}
	}

	if _, err := f.connector.ConnectGitHub(ctx, st.OrgID, cfg); err != nil {
		return st.ReturnTo, &CallbackError{ReasonConnectFailed, err}
	}
	return st.ReturnTo, nil
}

// verifyInstallation checks the user who completed the install can see the installation,
// then reads it as the app.
func (f *Flow) verifyInstallation(ctx context.Context, cb Callback) (models.IntegrationConfig, error) {
	if cb.Code == "" {
		return models.IntegrationConfig{}, errors.New("missing code")
	}
	installationID, err := strconv.ParseInt(cb.InstallationID, 10, 64)
	if err != nil || installationID == 0 {
		return models.IntegrationConfig{}, errors.New("missing installation_id")
	}

	userToken, err := f.github.exchangeCode(ctx, cb.Code)
	if err != nil {
		return models.IntegrationConfig{}, err
	}
	visible, err := f.github.userCanAccessInstallation(ctx, userToken, installationID)
	if err != nil {
		return models.IntegrationConfig{}, err
	}
	if !visible {
		return models.IntegrationConfig{}, errors.New("installation not visible to user")
	}

	inst, err := f.github.installation(ctx, installationID)
	if err != nil {
		return models.IntegrationConfig{}, err
	}
	return models.IntegrationConfig{
		InstallationID:      inst.ID,
		AccountLogin:        inst.Account.Login,
		AccountType:         inst.Account.Type,
		RepositorySelection: inst.RepositorySelection,
	}, nil
}

// SafeReturnTo keeps returnTo only if it is a path on our own UI, which stops the
// callback from being used as an open redirect. Browsers read "/\host" as "//host", so a
// backslash in second place is rejected too.
func SafeReturnTo(returnTo string) string {
	if !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") || strings.HasPrefix(returnTo, `/\`) {
		return DefaultReturnTo
	}
	return returnTo
}
