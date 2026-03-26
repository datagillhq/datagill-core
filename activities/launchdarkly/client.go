package launchdarkly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/shared"
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

func (c *Client) fetchAuditLogPage(ctx context.Context, url string) ([]models.LDAuditEntry, string, error) {
	var resp auditLogResponse
	if err := c.doRequest(ctx, url, &resp); err != nil {
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
		return &shared.ServiceError{Message: fmt.Sprintf("rate limited, retry after %ds", waitSeconds)}
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &shared.NonRetryableServiceError{Message: fmt.Sprintf("invalid API key (%d): %s", resp.StatusCode, string(body))}
	}

	if resp.StatusCode == http.StatusNotFound {
		return &shared.NonRetryableServiceError{Message: fmt.Sprintf("not found: %s", url)}
	}

	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &shared.ServiceError{Message: fmt.Sprintf("server error %d: %s", resp.StatusCode, string(body))}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &shared.ServiceError{Message: fmt.Sprintf("unexpected status %d: %s", resp.StatusCode, string(body))}
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
