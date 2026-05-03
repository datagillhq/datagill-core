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
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/datagill/datagill-core/models"
)

const (
	apiBaseURL       = "https://api.github.com"
	defaultTimeout   = 10 * time.Second
	maxErrorBodySize = 4096
	defaultRetryWait = 60
	searchMaxPerPage = 100
	searchResultCap  = 1000
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

// PullRequestDetailResponse preserves both the decoded PR detail and the exact
// response bytes GitHub returned for source_records replay.
type PullRequestDetailResponse struct {
	Detail models.GitHubPullRequestDetail
	Raw    json.RawMessage
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

// SearchPullRequests searches merged pull requests for one GitHub account within a time window.
func (c *Client) SearchPullRequests(ctx context.Context, installationToken, accountLogin, accountType string, from, to time.Time, page, perPage int) (models.GitHubSearchResponse, error) {
	if installationToken == "" {
		return models.GitHubSearchResponse{}, fmt.Errorf("installation token is required")
	}
	qualifier, err := accountQualifier(accountLogin, accountType)
	if err != nil {
		return models.GitHubSearchResponse{}, err
	}
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 || perPage > searchMaxPerPage {
		perPage = searchMaxPerPage
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/search/issues", nil)
	if err != nil {
		return models.GitHubSearchResponse{}, fmt.Errorf("create request: %w", err)
	}
	q := req.URL.Query()
	q.Set("q", fmt.Sprintf("is:pull-request is:merged %s merged:%s..%s", qualifier, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)))
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(perPage))
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+installationToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	var out models.GitHubSearchResponse
	if err := c.doJSON(req, &out); err != nil {
		return models.GitHubSearchResponse{}, fmt.Errorf("search pull requests: %w", err)
	}
	return out, nil
}

// GetPullRequestDetail fetches the full pull request payload for one discovered PR.
func (c *Client) GetPullRequestDetail(ctx context.Context, installationToken, owner, repo string, number int64) (PullRequestDetailResponse, error) {
	if installationToken == "" {
		return PullRequestDetailResponse{}, fmt.Errorf("installation token is required")
	}
	if owner == "" {
		return PullRequestDetailResponse{}, fmt.Errorf("owner is required")
	}
	if repo == "" {
		return PullRequestDetailResponse{}, fmt.Errorf("repo is required")
	}
	if number <= 0 {
		return PullRequestDetailResponse{}, fmt.Errorf("pull request number is required")
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return PullRequestDetailResponse{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+installationToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	var out models.GitHubPullRequestDetail
	raw, err := c.doRawJSON(req, &out)
	if err != nil {
		return PullRequestDetailResponse{}, fmt.Errorf("get pull request detail: %w", err)
	}
	return PullRequestDetailResponse{Detail: out, Raw: raw}, nil
}

func accountQualifier(accountLogin, accountType string) (string, error) {
	if accountLogin == "" {
		return "", fmt.Errorf("account login is required")
	}
	switch accountType {
	case "Organization":
		return "org:" + accountLogin, nil
	case "User":
		return "user:" + accountLogin, nil
	default:
		return "", fmt.Errorf("unsupported account type %q", accountType)
	}
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
	_, err := c.doRawJSON(req, out)
	return err
}

func (c *Client) doRawJSON(req *http.Request, out any) (json.RawMessage, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		waitSeconds := defaultRetryWait
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			waitSeconds = s
		}
		return nil, &TooManyRequestsError{RetryAfter: waitSeconds}
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return nil, &UnauthorizedError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}

	if resp.StatusCode == http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return nil, &NotFoundError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}

	if resp.StatusCode >= 500 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return nil, &UnexpectedStatusError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return json.RawMessage(raw), nil
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
