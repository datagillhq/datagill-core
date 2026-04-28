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

type GitHubAuthService interface {
	Start(ctx context.Context, orgID uuid.UUID, userID, returnTo string) (githubStartResponse, error)
	Complete(ctx context.Context, query url.Values) (string, error)
}

type githubAuthService struct {
	cfg        config
	repo       IntegrationsRepository
	orgRepo    OrgRepository
	scheduler  Scheduler
	httpClient *http.Client
	now        func() time.Time
	signer     *rsa.PrivateKey
}

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
		now:    time.Now,
		signer: signer,
	}
}

func (s *githubAuthService) Start(_ context.Context, orgID uuid.UUID, userID, returnTo string) (githubStartResponse, error) {
	if s.cfg.GitHubAppSlug == "" || s.cfg.GitHubAppStateSecret == "" {
		return githubStartResponse{}, fmt.Errorf("github auth is not configured")
	}

	state, expiresAt, err := s.signState(githubState{
		OrgID:     orgID,
		UserID:    userID,
		Nonce:     randomToken(16),
		ReturnTo:  normalizeReturnTo(returnTo),
		IssuedAt:  s.now().UTC(),
		ExpiresAt: s.now().UTC().Add(githubStateTTL),
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
	if stateToken == "" || code == "" {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, "/settings/integrations", "github_auth_failed"), fmt.Errorf("missing state or code")
	}

	state, err := s.verifyState(stateToken)
	if err != nil {
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, "/settings/integrations", "invalid_state"), err
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
	if err != nil || !visible {
		if err == nil {
			err = fmt.Errorf("installation not visible to installing user")
		}
		return buildGitHubErrorRedirect(s.cfg.FrontendURL, state.ReturnTo, "github_auth_failed"), err
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

	target, err := url.Parse(s.cfg.FrontendURL)
	if err != nil {
		return "/settings/integrations?provider=github&status=connected", nil
	}
	successTarget, err := target.Parse(state.ReturnTo)
	if err != nil {
		return "/settings/integrations?provider=github&status=connected", nil
	}
	q := successTarget.Query()
	q.Set("provider", "github")
	q.Set("status", "connected")
	successTarget.RawQuery = q.Encode()
	return successTarget.String(), nil
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubOAuthAccessToken, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBaseURL+"/user/installations", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	var resp struct {
		Installations []struct {
			ID int64 `json:"id"`
		} `json:"installations"`
	}
	if err := s.doJSON(req, &resp); err != nil {
		return false, fmt.Errorf("list user installations: %w", err)
	}
	for _, installation := range resp.Installations {
		if installation.ID == installationID {
			return true, nil
		}
	}
	return false, nil
}

func (s *githubAuthService) getInstallation(ctx context.Context, installationID int64) (struct {
	ID      int64
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	RepositorySelection string `json:"repository_selection"`
}, error) {
	var installation struct {
		ID      int64
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		RepositorySelection string `json:"repository_selection"`
	}
	jwtToken, err := s.appJWT()
	if err != nil {
		return installation, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/app/installations/%d", githubAPIBaseURL, installationID), nil)
	if err != nil {
		return installation, err
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	if err := s.doJSON(req, &installation); err != nil {
		return installation, fmt.Errorf("get installation: %w", err)
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
	return token.SignedString(s.signer)
}

func (s *githubAuthService) doJSON(req *http.Request, out any) error {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
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
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
