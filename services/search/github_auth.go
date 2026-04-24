package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
)

const (
	githubStateTTL   = 15 * time.Minute
	githubAPIBaseURL = "https://api.github.com"
	githubOAuthURL   = "https://github.com/login/oauth/access_token"
)

type githubAuthStateClaims struct {
	OrgID    string `json:"org_id"`
	UserID   string `json:"user_id"`
	ReturnTo string `json:"return_to"`
	Nonce    string `json:"nonce"`
	jwt.RegisteredClaims
}

type githubAuthStartRequest struct {
	ReturnTo string `json:"return_to"`
}

type githubAuthStartResponse struct {
	InstallURL string    `json:"install_url"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type githubAuthCallbackResponse struct {
	Code           string                     `json:"code,omitempty"`
	State          string                     `json:"state,omitempty"`
	InstallationID string                     `json:"installation_id,omitempty"`
	SetupAction    string                     `json:"setup_action,omitempty"`
	Query          map[string]string          `json:"query"`
	DecodedState   *githubAuthStatePayload    `json:"decoded_state,omitempty"`
	Exchange       *githubOAuthExchangeResult `json:"exchange,omitempty"`
	Verification   *githubInstallationCheck   `json:"verification,omitempty"`
	Installation   *githubInstallationSummary `json:"installation,omitempty"`
	SearchProbe    *githubSearchProbe         `json:"search_probe,omitempty"`
	Warnings       []string                   `json:"warnings,omitempty"`
}

type githubAuthStatePayload struct {
	OrgID     string    `json:"org_id"`
	UserID    string    `json:"user_id"`
	ReturnTo  string    `json:"return_to"`
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
}

type githubOAuthExchangeResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

type githubOAuthExchangeResult struct {
	TokenType string `json:"token_type,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

type githubUserInstallationsResponse struct {
	Installations []githubUserInstallation `json:"installations"`
}

type githubUserInstallation struct {
	ID      int64               `json:"id"`
	Account githubAccountDetail `json:"account"`
}

type githubAppInstallation struct {
	ID                  int64               `json:"id"`
	Account             githubAccountDetail `json:"account"`
	RepositorySelection string              `json:"repository_selection"`
}

type githubAccountDetail struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type githubInstallationCheck struct {
	VisibleToUser bool   `json:"visible_to_user"`
	MatchedID     int64  `json:"matched_id,omitempty"`
	AccountLogin  string `json:"account_login,omitempty"`
	AccountType   string `json:"account_type,omitempty"`
}

type githubInstallationSummary struct {
	ID                  int64  `json:"id"`
	AccountLogin        string `json:"account_login"`
	AccountType         string `json:"account_type"`
	RepositorySelection string `json:"repository_selection"`
}

type githubInstallationTokenResponse struct {
	Token string `json:"token"`
}

type githubSearchIssuesResponse struct {
	TotalCount        int  `json:"total_count"`
	IncompleteResults bool `json:"incomplete_results"`
	Items             []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"items"`
}

type githubSearchProbe struct {
	Qualifier         string `json:"qualifier"`
	StatusCode        int    `json:"status_code"`
	TotalCount        int    `json:"total_count,omitempty"`
	IncompleteResults bool   `json:"incomplete_results,omitempty"`
	ItemsReturned     int    `json:"items_returned,omitempty"`
	Error             string `json:"error,omitempty"`
}

func (a *app) githubAuthStartHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	if a.config.GitHubAppSlug == "" || a.config.GitHubStateKey == "" {
		log.Error().Msg("github app auth is not configured")
		writeCode(w, http.StatusNotImplemented, "GITHUB_AUTH_NOT_CONFIGURED")
		return
	}

	var req githubAuthStartRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST_BODY")
		return
	}
	if req.ReturnTo == "" {
		req.ReturnTo = "/settings/integrations"
	}

	orgID, userID, ok := a.githubAuthSubject(r)
	if !ok {
		writeCode(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}

	state, expiresAt, err := a.signGitHubState(orgID, userID, req.ReturnTo)
	if err != nil {
		log.Error().Err(err).Msg("failed to sign github state")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	installURL := fmt.Sprintf("https://github.com/apps/%s/installations/new?state=%s", a.config.GitHubAppSlug, url.QueryEscape(state))
	writeJSON(w, http.StatusOK, githubAuthStartResponse{
		InstallURL: installURL,
		ExpiresAt:  expiresAt,
	})
}

func (a *app) githubAuthSubject(r *http.Request) (orgID, userID string, ok bool) {
	orgID = orgIDFromCtx(r)
	userID = userIDFromCtx(r)
	if orgID != "" && userID != "" {
		return orgID, userID, true
	}
	if a.config.GitHubSpikeMode {
		return "spike-org", "spike-user", true
	}
	return "", "", false
}

func (a *app) githubAuthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	state := r.URL.Query().Get("state")
	if state == "" {
		writeCode(w, http.StatusBadRequest, "GITHUB_AUTH_STATE_REQUIRED")
		return
	}

	claims, err := a.parseGitHubState(state)
	if err != nil {
		log.Warn().Err(err).Msg("invalid github callback state")
		writeCode(w, http.StatusBadRequest, "GITHUB_AUTH_INVALID_STATE")
		return
	}

	resp := githubAuthCallbackResponse{
		Code:           r.URL.Query().Get("code"),
		State:          state,
		InstallationID: r.URL.Query().Get("installation_id"),
		SetupAction:    r.URL.Query().Get("setup_action"),
		Query:          firstQueryValues(r.URL.Query()),
		DecodedState: &githubAuthStatePayload{
			OrgID:     claims.OrgID,
			UserID:    claims.UserID,
			ReturnTo:  claims.ReturnTo,
			Nonce:     claims.Nonce,
			ExpiresAt: claims.ExpiresAt.Time,
		},
	}

	if resp.Code == "" {
		resp.Warnings = append(resp.Warnings, "missing code; skipping live token exchange")
		writeJSON(w, http.StatusOK, resp)
		return
	}

	userToken, exchangeResult, err := a.exchangeGitHubCode(r.Context(), resp.Code)
	if err != nil {
		resp.Warnings = append(resp.Warnings, "token exchange failed: "+err.Error())
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Exchange = exchangeResult

	installationID, err := parseInstallationID(resp.InstallationID)
	if err != nil {
		resp.Warnings = append(resp.Warnings, "invalid installation_id: "+err.Error())
		writeJSON(w, http.StatusOK, resp)
		return
	}

	verification, err := a.verifyUserInstallation(r.Context(), userToken, installationID)
	if err != nil {
		resp.Warnings = append(resp.Warnings, "user installation verification failed: "+err.Error())
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Verification = verification

	installation, err := a.getAppInstallation(r.Context(), installationID)
	if err != nil {
		resp.Warnings = append(resp.Warnings, "app installation lookup failed: "+err.Error())
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Installation = &githubInstallationSummary{
		ID:                  installation.ID,
		AccountLogin:        installation.Account.Login,
		AccountType:         installation.Account.Type,
		RepositorySelection: installation.RepositorySelection,
	}

	installationToken, err := a.createInstallationToken(r.Context(), installationID)
	if err != nil {
		resp.Warnings = append(resp.Warnings, "installation token creation failed: "+err.Error())
		writeJSON(w, http.StatusOK, resp)
		return
	}

	probe, err := a.probeSearchIssues(r.Context(), installationToken, installation.Account.Login, installation.Account.Type)
	if err != nil {
		resp.Warnings = append(resp.Warnings, "search probe failed: "+err.Error())
	} else {
		resp.SearchProbe = probe
	}

	writeJSON(w, http.StatusOK, resp)
}

func (a *app) signGitHubState(orgID, userID, returnTo string) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(githubStateTTL)
	claims := githubAuthStateClaims{
		OrgID:    orgID,
		UserID:   userID,
		ReturnTo: returnTo,
		Nonce:    randomNonce(),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(a.config.GitHubStateKey))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign github state: %w", err)
	}
	return signed, expiresAt, nil
}

func (a *app) parseGitHubState(raw string) (*githubAuthStateClaims, error) {
	token, err := jwt.ParseWithClaims(raw, &githubAuthStateClaims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(a.config.GitHubStateKey), nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*githubAuthStateClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid github state claims")
	}
	return claims, nil
}

func (a *app) exchangeGitHubCode(ctx context.Context, code string) (string, *githubOAuthExchangeResult, error) {
	if a.config.GitHubClientID == "" || a.config.GitHubClientSecret == "" {
		return "", nil, errors.New("missing GITHUB_APP_CLIENT_ID or GITHUB_APP_CLIENT_SECRET")
	}

	form := url.Values{}
	form.Set("client_id", a.config.GitHubClientID)
	form.Set("client_secret", a.config.GitHubClientSecret)
	form.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubOAuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var payload githubOAuthExchangeResponse
	status, err := doJSON(req, &payload)
	if err != nil {
		return "", nil, fmt.Errorf("exchange code (status %d): %w", status, err)
	}
	if payload.AccessToken == "" {
		return "", nil, errors.New("missing access_token in oauth exchange response")
	}

	return payload.AccessToken, &githubOAuthExchangeResult{
		TokenType: payload.TokenType,
		Scope:     payload.Scope,
	}, nil
}

func (a *app) verifyUserInstallation(ctx context.Context, userToken string, installationID int64) (*githubInstallationCheck, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBaseURL+"/user/installations", nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req, userToken)

	var payload githubUserInstallationsResponse
	status, err := doJSON(req, &payload)
	if err != nil {
		return nil, fmt.Errorf("list user installations (status %d): %w", status, err)
	}

	check := &githubInstallationCheck{}
	for _, installation := range payload.Installations {
		if installation.ID == installationID {
			check.VisibleToUser = true
			check.MatchedID = installation.ID
			check.AccountLogin = installation.Account.Login
			check.AccountType = installation.Account.Type
			return check, nil
		}
	}
	return check, nil
}

func (a *app) getAppInstallation(ctx context.Context, installationID int64) (*githubAppInstallation, error) {
	jwtToken, err := a.signGitHubAppJWT()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/app/installations/%d", githubAPIBaseURL, installationID), nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req, jwtToken)

	var payload githubAppInstallation
	status, err := doJSON(req, &payload)
	if err != nil {
		return nil, fmt.Errorf("get app installation (status %d): %w", status, err)
	}
	return &payload, nil
}

func (a *app) createInstallationToken(ctx context.Context, installationID int64) (string, error) {
	jwtToken, err := a.signGitHubAppJWT()
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPIBaseURL, installationID), bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return "", err
	}
	setGitHubHeaders(req, jwtToken)

	var payload githubInstallationTokenResponse
	status, err := doJSON(req, &payload)
	if err != nil {
		return "", fmt.Errorf("create installation token (status %d): %w", status, err)
	}
	if payload.Token == "" {
		return "", errors.New("missing installation token")
	}
	return payload.Token, nil
}

func (a *app) probeSearchIssues(ctx context.Context, installationToken, accountLogin, accountType string) (*githubSearchProbe, error) {
	qualifier := "org:" + accountLogin
	if strings.EqualFold(accountType, "user") {
		qualifier = "user:" + accountLogin
	}
	searchURL := fmt.Sprintf("%s/search/issues?q=is:pull-request+is:merged+%s&per_page=1", githubAPIBaseURL, url.QueryEscape(qualifier))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req, installationToken)

	var payload githubSearchIssuesResponse
	status, err := doJSON(req, &payload)
	probe := &githubSearchProbe{
		Qualifier:  qualifier,
		StatusCode: status,
	}
	if err != nil {
		probe.Error = err.Error()
		return probe, nil
	}
	probe.TotalCount = payload.TotalCount
	probe.IncompleteResults = payload.IncompleteResults
	probe.ItemsReturned = len(payload.Items)
	return probe, nil
}

func (a *app) signGitHubAppJWT() (string, error) {
	if a.config.GitHubAppID == "" || a.config.GitHubPrivateKeyB64 == "" {
		return "", errors.New("missing GITHUB_APP_ID or GITHUB_APP_PRIVATE_KEY_B64")
	}

	rawKey, err := base64.StdEncoding.DecodeString(a.config.GitHubPrivateKeyB64)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	block, _ := pem.Decode(rawKey)
	if block == nil {
		return "", errors.New("invalid PEM private key")
	}
	parsedKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		pkcs8Key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return "", fmt.Errorf("parse private key: %w", err)
		}
		rsaKey, ok := pkcs8Key.(*rsa.PrivateKey)
		if !ok {
			return "", errors.New("private key is not RSA")
		}
		parsedKey = rsaKey
	}

	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{
		Issuer:    a.config.GitHubAppID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-1 * time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(parsedKey)
}

func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func doJSON(req *http.Request, out any) (int, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return resp.StatusCode, fmt.Errorf("unexpected status: %s body=%q", resp.Status, string(body))
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func parseInstallationID(raw string) (int64, error) {
	if raw == "" {
		return 0, errors.New("missing installation_id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

func firstQueryValues(values url.Values) map[string]string {
	out := map[string]string{}
	for key, v := range values {
		if len(v) > 0 {
			out[key] = v[0]
		}
	}
	return out
}

func decodeJSONBody(r *http.Request, out any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	return json.NewDecoder(r.Body).Decode(out)
}

func randomNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
