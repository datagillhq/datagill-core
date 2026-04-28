package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	githubStateTTL      = 15 * time.Minute
	githubInstallURLFmt = "https://github.com/apps/%s/installations/new"
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

type githubAuthService struct {
	cfg config
	now func() time.Time
}

func newGitHubAuthService(cfg config) GitHubAuthService {
	return &githubAuthService{
		cfg: cfg,
		now: time.Now,
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

func normalizeReturnTo(returnTo string) string {
	if returnTo == "" || !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") {
		return "/settings/integrations"
	}
	return returnTo
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
