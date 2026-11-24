package githubapp

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	defaultOAuthURL   = "https://github.com/login/oauth/access_token"
	installationsPage = 100
)

// installation is the part of GitHub's installation object the flow stores.
type installation struct {
	ID                  int64  `json:"id"`
	RepositorySelection string `json:"repository_selection"`
	Account             struct {
		Login string `json:"login"`
		Type  string `json:"type"` // "User" or "Organization"
	} `json:"account"`
}

// client calls the GitHub endpoints the install flow needs, as the user (OAuth) and as
// the app (app JWT).
type client struct {
	http         *http.Client
	oauthURL     string
	apiBaseURL   string
	appID        string
	clientID     string
	clientSecret string
	callbackURL  string
	appKey       *rsa.PrivateKey // nil when the app isn't configured
	now          func() time.Time
}

// exchangeCode trades the OAuth code from the callback for a user access token.
func (c *client) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"code":          {code},
	}
	if c.callbackURL != "" {
		form.Set("redirect_uri", c.callbackURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build oauth request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.do(req, &out); err != nil {
		return "", fmt.Errorf("exchange oauth code: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("exchange oauth code: missing access token")
	}
	return out.AccessToken, nil
}

// userCanAccessInstallation reports whether the user behind userToken can see the
// installation. Without this check, anyone could attach someone else's installation ID
// to their own org.
func (c *client) userCanAccessInstallation(ctx context.Context, userToken string, installationID int64) (bool, error) {
	for page := 1; ; page++ {
		reqURL := fmt.Sprintf("%s/user/installations?per_page=%d&page=%d", c.apiBaseURL, installationsPage, page)
		var out struct {
			Installations []installation `json:"installations"`
		}
		if err := c.get(ctx, reqURL, userToken, &out); err != nil {
			return false, fmt.Errorf("list user installations: %w", err)
		}
		for _, inst := range out.Installations {
			if inst.ID == installationID {
				return true, nil
			}
		}
		if len(out.Installations) < installationsPage {
			return false, nil
		}
	}
}

// installation fetches an installation as the app.
func (c *client) installation(ctx context.Context, installationID int64) (installation, error) {
	token, err := c.appJWT()
	if err != nil {
		return installation{}, err
	}
	var out installation
	if err := c.get(ctx, fmt.Sprintf("%s/app/installations/%d", c.apiBaseURL, installationID), token, &out); err != nil {
		return installation{}, fmt.Errorf("get installation: %w", err)
	}
	return out, nil
}

// appJWT signs a short-lived JWT that authenticates as the GitHub App. iat is backdated a
// minute to tolerate clock drift; GitHub caps exp at ten minutes.
func (c *client) appJWT() (string, error) {
	if c.appID == "" || c.appKey == nil {
		return "", ErrNotConfigured
	}
	now := c.now().UTC()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": c.appID,
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
	})
	signed, err := token.SignedString(c.appKey)
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signed, nil
}

func (c *client) get(ctx context.Context, reqURL, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	return c.do(req, out)
}

func (c *client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// parsePrivateKey accepts the PKCS#1 key GitHub issues as well as PKCS#8.
func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return key, nil
}
