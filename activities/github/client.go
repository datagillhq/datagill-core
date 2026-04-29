package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	apiBaseURL       = "https://api.github.com"
	defaultTimeout   = 10 * time.Second
	maxErrorBodySize = 4096
	defaultRetryWait = 60
)

// Client is a thin HTTP wrapper for GitHub App authentication.
// It signs app JWTs and exchanges them for installation access tokens.
type Client struct {
	httpClient *http.Client
	appID      string
	signer     *rsa.PrivateKey
	baseURL    string
	now        func() time.Time
}

// ClientOption configures the Client.
type ClientOption func(*Client)

// WithBaseURL overrides the default GitHub API base URL.
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// WithNow overrides time.Now for tests.
func WithNow(now func() time.Time) ClientOption {
	return func(c *Client) {
		c.now = now
	}
}

// InstallationToken is the short-lived token returned by GitHub for one installation.
type InstallationToken struct {
	Token     string
	ExpiresAt time.Time
}

type installationTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewClient creates a new GitHub App client.
func NewClient(appID string, privateKeyPEM []byte, opts ...ClientOption) (*Client, error) {
	if appID == "" {
		return nil, fmt.Errorf("app id is required")
	}
	signer, err := parseGitHubPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}

	c := &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		appID:      appID,
		signer:     signer,
		baseURL:    apiBaseURL,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// CreateInstallationToken exchanges an app JWT for an installation token.
func (c *Client) CreateInstallationToken(ctx context.Context, installationID int64) (InstallationToken, error) {
	if installationID == 0 {
		return InstallationToken{}, fmt.Errorf("installation id is required")
	}

	appJWT, err := c.appJWT()
	if err != nil {
		return InstallationToken{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL, installationID), nil)
	if err != nil {
		return InstallationToken{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	var out installationTokenResponse
	if err := c.doJSON(req, &out); err != nil {
		return InstallationToken{}, fmt.Errorf("create installation token: %w", err)
	}
	if out.Token == "" {
		return InstallationToken{}, fmt.Errorf("create installation token: missing token")
	}
	return InstallationToken{Token: out.Token, ExpiresAt: out.ExpiresAt}, nil
}

func (c *Client) appJWT() (string, error) {
	now := c.now().UTC()
	claims := jwt.MapClaims{
		"iss": c.appID,
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(c.signer)
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signed, nil
}

func (c *Client) doJSON(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		waitSeconds := defaultRetryWait
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			waitSeconds = s
		}
		return &TooManyRequestsError{RetryAfter: waitSeconds}
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &UnauthorizedError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}

	if resp.StatusCode == http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &NotFoundError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}

	if resp.StatusCode >= 500 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &UnexpectedStatusError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
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
