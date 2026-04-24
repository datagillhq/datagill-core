package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
)

const githubStateTTL = 15 * time.Minute

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
	Code           string                  `json:"code,omitempty"`
	State          string                  `json:"state,omitempty"`
	InstallationID string                  `json:"installation_id,omitempty"`
	SetupAction    string                  `json:"setup_action,omitempty"`
	Query          map[string]string       `json:"query"`
	DecodedState   *githubAuthStatePayload `json:"decoded_state,omitempty"`
}

type githubAuthStatePayload struct {
	OrgID     string    `json:"org_id"`
	UserID    string    `json:"user_id"`
	ReturnTo  string    `json:"return_to"`
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
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

	state, expiresAt, err := a.signGitHubState(orgIDFromCtx(r), userIDFromCtx(r), req.ReturnTo)
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

	query := map[string]string{}
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			query[key] = values[0]
		}
	}

	writeJSON(w, http.StatusOK, githubAuthCallbackResponse{
		Code:           r.URL.Query().Get("code"),
		State:          state,
		InstallationID: r.URL.Query().Get("installation_id"),
		SetupAction:    r.URL.Query().Get("setup_action"),
		Query:          query,
		DecodedState: &githubAuthStatePayload{
			OrgID:     claims.OrgID,
			UserID:    claims.UserID,
			ReturnTo:  claims.ReturnTo,
			Nonce:     claims.Nonce,
			ExpiresAt: claims.ExpiresAt.Time,
		},
	})
}

func (a *app) signGitHubState(orgID, userID, returnTo string) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(githubStateTTL)
	claims := githubAuthStateClaims{
		OrgID:    orgID,
		UserID:   userID,
		ReturnTo: returnTo,
		Nonce:    fmt.Sprintf("%d", now.UnixNano()),
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

func decodeJSONBody(r *http.Request, out any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	return json.NewDecoder(r.Body).Decode(out)
}
