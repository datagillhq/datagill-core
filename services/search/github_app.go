package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

const (
	githubAPIBaseURL       = "https://api.github.com"
	githubOAuthAccessToken = "https://github.com/login/oauth/access_token"
	githubStateTTL         = 15 * time.Minute
	githubInstallURLFmt    = "https://github.com/apps/%s/installations/new"
)

type githubStartResponse struct {
	InstallURL string    `json:"install_url"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type githubState struct {
	OrgID     uuid.UUID `json:"org_id"`
	UserID    string    `json:"user_id"`
	Nonce     string    `json:"nonce"`
	ReturnTo  string    `json:"return_to"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type githubOAuthTokenResponse struct {
	AccessToken string `json:"access_token"`
}

type githubUserInstallationsResponse struct {
	Installations []githubInstallation `json:"installations"`
}

type githubInstallation struct {
	ID                  int64          `json:"id"`
	Account             githubAccount  `json:"account"`
	RepositorySelection string         `json:"repository_selection"`
}

type githubAccount struct {
	Login string `json:"login"`
	Type  string `json:"type"` // "User" or "Organization"
}

type githubAuthService struct {
	cfg        config
	repo       IntegrationsRepository
	orgRepo    OrgRepository
	scheduler  Scheduler
	httpClient *http.Client
	now        func() time.Time
	signer     *rsa.PrivateKey
	oauthURL   string
	apiBaseURL string
}

func newGitHubAuthService(cfg config, repo IntegrationsRepository, orgRepo OrgRepository, scheduler Scheduler) GitHubAuthService {
	signer, _ := parseGitHubPrivateKey(cfg.GitHubAppPrivateKey)
	return &githubAuthService{
		cfg:       cfg,
		repo:      repo,
		orgRepo:   orgRepo,
		scheduler: scheduler,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		now:        time.Now,
		signer:     signer,
		oauthURL:   githubOAuthAccessToken,
		apiBaseURL: githubAPIBaseURL,
	}
}

func (s *githubAuthService) Start(_ context.Context, orgID uuid.UUID, userID, returnTo string) (githubStartResponse, error) {
	if s.cfg.GitHubAppSlug == "" || s.cfg.GitHubAppStateSecret == "" {
		return githubStartResponse{}, fmt.Errorf("github auth is not configured")
	}

	now := s.now().UTC()
	state, expiresAt, err := s.signState(githubState{
		OrgID:     orgID,
		UserID:    userID,
		Nonce:     randomToken(16),
		ReturnTo:  normalizeReturnTo(returnTo),
		IssuedAt:  now,
		ExpiresAt: now.Add(githubStateTTL),
	})
	if err != nil {
		return githubStartResponse{}, err
	}

	installURL, err := url.Parse(fmt.Sprintf(githubInstallURLFmt, s.cfg.GitHubAppSlug))
	if err != nil {
		return githubStartResponse{}, fmt.Errorf("build install url: %w", err)
	}
	q := installURL.Query()
	q.Set("state", state)
	q.Set("request_oauth_on_install", "true")
	installURL.RawQuery = q.Encode()

	return githubStartResponse{
		InstallURL: installURL.String(),
		ExpiresAt:  expiresAt,
	}, nil
}

func (s *githubAuthService) Complete(ctx context.Context, query url.Values) (string, error) {
	stateToken := query.Get("state")
	code := query.Get("code")
	if stateToken == "" {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, "/settings/integrations", "invalid_state"), fmt.Errorf("missing state")
	}
	state, err := s.verifyState(stateToken)
	if err != nil {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, "/settings/integrations", "invalid_state"), err
	}
	if code == "" {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "github_auth_failed"), fmt.Errorf("missing code")
	}

	installationID, err := strconv.ParseInt(query.Get("installation_id"), 10, 64)
	if err != nil || installationID == 0 {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "github_auth_failed"), fmt.Errorf("missing installation_id")
	}

	userToken, err := s.exchangeCode(ctx, code)
	if err != nil {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "github_auth_failed"), err
	}

	visible, err := s.userCanAccessInstallation(ctx, userToken, installationID)
	if err != nil {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "github_auth_failed"), err
	}
	if !visible {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "github_auth_failed"), fmt.Errorf("installation not visible to user")
	}

	installation, err := s.getInstallation(ctx, installationID)
	if err != nil {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "github_auth_failed"), err
	}

	if err := s.orgRepo.CreateOrg(ctx, state.OrgID, state.OrgID.String()); err != nil {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "integration_create_failed"), err
	}

	integration, created, err := s.repo.UpsertGitHubIntegration(ctx, state.OrgID, models.IntegrationConfig{
		InstallationID:      installation.ID,
		AccountLogin:        installation.Account.Login,
		AccountType:         installation.Account.Type,
		RepositorySelection: installation.RepositorySelection,
	})
	if err != nil {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "integration_create_failed"), err
	}

	if created {
		if err := s.scheduler.Create(ctx, integration); err != nil {
			return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "integration_create_failed"), err
		}
	}

	return buildGitHubSuccessRedirect(s.cfg.FrontendURL, state.ReturnTo), nil
}

func (s *githubAuthService) signState(state githubState) (string, time.Time, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("marshal state: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.GitHubAppStateSecret))
	mac.Write(payload)
	sig := mac.Sum(nil)
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
	return token, state.ExpiresAt, nil
}

func (s *githubAuthService) verifyState(token string) (githubState, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return githubState{}, fmt.Errorf("invalid state format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return githubState{}, fmt.Errorf("decode state payload: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return githubState{}, fmt.Errorf("decode state signature: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(s.cfg.GitHubAppStateSecret))
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return githubState{}, fmt.Errorf("invalid state signature")
	}

	var state githubState
	if err := json.Unmarshal(payload, &state); err != nil {
		return githubState{}, fmt.Errorf("unmarshal state: %w", err)
	}
	if s.now().UTC().After(state.ExpiresAt) {
		return githubState{}, fmt.Errorf("state expired")
	}
	state.ReturnTo = normalizeReturnTo(state.ReturnTo)
	return state, nil
}

func (s *githubAuthService) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", s.cfg.GitHubAppClientID)
	form.Set("client_secret", s.cfg.GitHubAppClientSecret)
	form.Set("code", code)
	if s.cfg.GitHubAppCallbackURL != "" {
		form.Set("redirect_uri", s.cfg.GitHubAppCallbackURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.oauthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build oauth request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var out githubOAuthTokenResponse
	if err := s.doJSON(req, &out); err != nil {
		return "", fmt.Errorf("exchange oauth code: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("exchange oauth code: missing access token")
	}
	return out.AccessToken, nil
}

func (s *githubAuthService) userCanAccessInstallation(ctx context.Context, userToken string, installationID int64) (bool, error) {
	for page := 1; ; page++ {
		reqURL := fmt.Sprintf("%s/user/installations?per_page=100&page=%d", s.apiBaseURL, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return false, fmt.Errorf("build user installations request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+userToken)
		req.Header.Set("Accept", "application/vnd.github+json")

		var resp githubUserInstallationsResponse
		if err := s.doJSON(req, &resp); err != nil {
			return false, fmt.Errorf("list user installations: %w", err)
		}
		for _, installation := range resp.Installations {
			if installation.ID == installationID {
				return true, nil
			}
		}
		if len(resp.Installations) < 100 {
			return false, nil
		}
	}
}

func (s *githubAuthService) getInstallation(ctx context.Context, installationID int64) (githubInstallation, error) {
	jwtToken, err := s.appJWT()
	if err != nil {
		return githubInstallation{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/app/installations/%d", s.apiBaseURL, installationID), nil)
	if err != nil {
		return githubInstallation{}, fmt.Errorf("build installation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	var installation githubInstallation
	if err := s.doJSON(req, &installation); err != nil {
		return githubInstallation{}, fmt.Errorf("get installation: %w", err)
	}
	return installation, nil
}

func (s *githubAuthService) appJWT() (string, error) {
	if s.cfg.GitHubAppID == "" || s.signer == nil {
		return "", fmt.Errorf("github app credentials are not configured")
	}
	now := s.now().UTC()
	claims := jwt.MapClaims{
		"iss": s.cfg.GitHubAppID,
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(s.signer)
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signed, nil
}

func (s *githubAuthService) doJSON(req *http.Request, out any) error {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode json response: %w", err)
	}
	return nil
}

func buildGitHubSuccessRedirect(frontendURL, returnTo string) string {
	return buildGitHubRedirect(frontendURL, returnTo, "connected", "")
}

func buildGitHubErrorRedirect(frontendURL, returnTo, code string) string {
	return buildGitHubRedirect(frontendURL, returnTo, "error", code)
}

func buildGitHubRedirect(frontendURL, returnTo, status, code string) string {
	base, err := url.Parse(frontendURL)
	if err != nil {
		if code == "" {
			return "/settings/integrations?provider=github&status=connected"
		}
		return "/settings/integrations?provider=github&status=error&code=" + code
	}
	target, err := base.Parse(normalizeReturnTo(returnTo))
	if err != nil {
		if code == "" {
			return "/settings/integrations?provider=github&status=connected"
		}
		return "/settings/integrations?provider=github&status=error&code=" + code
	}
	q := target.Query()
	q.Set("provider", "github")
	q.Set("status", status)
	if code != "" {
		q.Set("code", code)
	}
	target.RawQuery = q.Encode()
	return target.String()
}

func normalizeReturnTo(returnTo string) string {
	if returnTo == "" || !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") {
		return "/settings/integrations"
	}
	return returnTo
}

func parseGitHubPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	if len(pemBytes) == 0 {
		return nil, fmt.Errorf("missing private key")
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("decode pem block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}
	return key, nil
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
