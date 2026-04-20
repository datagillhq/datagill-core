package launchdarkly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/datagill/datagill-core/models"
)

const (
	apiBaseURL       = "https://app.launchdarkly.com/api/v2"
	defaultTimeout   = 10 * time.Second
	maxPageSize      = 20
	maxErrorBodySize = 4096
	defaultRetryWait = 60
)

// Client is a thin HTTP wrapper for the LaunchDarkly API.
// It handles authentication, timeouts, and error classification for Temporal retry policies.
type Client struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

// ClientOption configures the Client.
type ClientOption func(*Client)

// WithBaseURL overrides the default API base URL (for testing).
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

// NewClient creates a new Client with the given API key.
func NewClient(apiKey string, opts ...ClientOption) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	c := &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		apiKey:     apiKey,
		baseURL:    apiBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// auditLogResponse is the paginated response from GET /auditlog.
type auditLogResponse struct {
	Items []models.LDAuditEntry `json:"items"`
	Links struct {
		Next *struct {
			Href string `json:"href"`
		} `json:"next,omitempty"`
	} `json:"_links"`
}

// ListAuditLog fetches one page of audit log entries filtered by time range.
// Use zero time.Time values to omit before/after. Returns entries and the next
// page URL (empty if no more pages). Pass the next URL to ListAuditLogPage to paginate.
func (c *Client) ListAuditLog(ctx context.Context, before, after time.Time, limit int) ([]models.LDAuditEntry, string, error) {
	if limit <= 0 || limit > maxPageSize {
		limit = maxPageSize
	}

	url := fmt.Sprintf("%s/auditlog?limit=%d", c.baseURL, limit)
	if !before.IsZero() {
		url += fmt.Sprintf("&before=%d", before.UnixMilli())
	}
	if !after.IsZero() {
		url += fmt.Sprintf("&after=%d", after.UnixMilli())
	}

	return c.fetchAuditLogPage(ctx, url)
}

// ListAuditLogPage fetches a page of audit log entries from a URL returned by a previous call.
func (c *Client) ListAuditLogPage(ctx context.Context, pageURL string) ([]models.LDAuditEntry, string, error) {
	return c.fetchAuditLogPage(ctx, pageURL)
}

func (c *Client) fetchAuditLogPage(ctx context.Context, pageURL string) ([]models.LDAuditEntry, string, error) {
	// LD returns relative paths in _links.next.href (e.g. "/api/v2/auditlog?before=...").
	// Resolve against the base URL so the HTTP client gets a fully-qualified URL.
	if u, err := url.Parse(pageURL); err == nil && !u.IsAbs() {
		base, _ := url.Parse(c.baseURL)
		pageURL = base.ResolveReference(u).String()
	}

	var resp auditLogResponse
	if err := c.doRequest(ctx, pageURL, &resp); err != nil {
		return nil, "", fmt.Errorf("ListAuditLog: %w", err)
	}

	var nextURL string
	if resp.Links.Next != nil {
		nextURL = resp.Links.Next.Href
	}

	return resp.Items, nextURL, nil
}

// GetFlagDetail fetches the full flag configuration for a specific flag.
func (c *Client) GetFlagDetail(ctx context.Context, projectKey, flagKey string) (models.LDFlagDetail, error) {
	url := fmt.Sprintf("%s/flags/%s/%s", c.baseURL, projectKey, flagKey)

	var flag models.LDFlagDetail
	if err := c.doRequest(ctx, url, &flag); err != nil {
		return models.LDFlagDetail{}, fmt.Errorf("GetFlagDetail %s/%s: %w", projectKey, flagKey, err)
	}

	return flag, nil
}

func (c *Client) doRequest(ctx context.Context, url string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // close error is not actionable for HTTP responses

	if resp.StatusCode == http.StatusTooManyRequests {
		waitSeconds := defaultRetryWait
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			waitSeconds = s
		}
		return &TooManyRequestsError{RetryAfter: waitSeconds}
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &UnauthorizedError{StatusCode: resp.StatusCode, Message: string(body)}
	}

	if resp.StatusCode == http.StatusNotFound {
		return &NotFoundError{URL: url}
	}

	if resp.StatusCode >= 500 || resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &UnexpectedStatusError{StatusCode: resp.StatusCode, Message: string(body)}
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// TooManyRequestsError is returned when the API rate limit is exceeded.
// RetryAfter indicates how many seconds to wait before retrying.
type TooManyRequestsError struct {
	RetryAfter int
}

func (e *TooManyRequestsError) Error() string {
	return fmt.Sprintf("rate limited, retry after %ds", e.RetryAfter)
}

// UnauthorizedError is returned when the API key is invalid or lacks permissions.
type UnauthorizedError struct {
	StatusCode int
	Message    string
}

func (e *UnauthorizedError) Error() string {
	return fmt.Sprintf("unauthorized (%d): %s", e.StatusCode, e.Message)
}

// NotFoundError is returned when the requested resource does not exist.
type NotFoundError struct {
	URL string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("not found: %s", e.URL)
}

// UnexpectedStatusError is returned for any other non-200 response.
type UnexpectedStatusError struct {
	StatusCode int
	Message    string
}

func (e *UnexpectedStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, e.Message)
}
