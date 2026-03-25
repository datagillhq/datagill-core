package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/datagill/datagill-core/models"
)

const (
	ldAPIBaseURL       = "https://app.launchdarkly.com/api/v2"
	ldDefaultTimeout   = 10 * time.Second
	ldMaxPageSize      = 20
	ldMaxErrorBodySize = 4096 // Max bytes to read from error response bodies
	ldDefaultRetryWait = 60   // Default seconds to wait on rate limit if no Retry-After header
)

// LaunchDarklyClient is a thin HTTP wrapper for the LaunchDarkly API.
// It handles authentication, timeouts, and error classification for Temporal retry policies.
type LaunchDarklyClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

// LaunchDarklyClientOption configures the client.
type LaunchDarklyClientOption func(*LaunchDarklyClient)

// WithBaseURL overrides the default API base URL (for testing).
func WithBaseURL(url string) LaunchDarklyClientOption {
	return func(c *LaunchDarklyClient) {
		c.baseURL = url
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) LaunchDarklyClientOption {
	return func(c *LaunchDarklyClient) {
		c.httpClient = client
	}
}

// NewLaunchDarklyClient creates a new client with the given API key.
// Returns an error if the API key is empty.
func NewLaunchDarklyClient(apiKey string, opts ...LaunchDarklyClientOption) (*LaunchDarklyClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	c := &LaunchDarklyClient{
		httpClient: &http.Client{Timeout: ldDefaultTimeout},
		apiKey:     apiKey,
		baseURL:    ldAPIBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// LDAuditLogResponse is the paginated response from GET /auditlog.
type LDAuditLogResponse struct {
	Items []models.LDAuditEntry `json:"items"`
	Links struct {
		Next *struct {
			Href string `json:"href"`
		} `json:"next,omitempty"`
	} `json:"_links"`
}

// ListAuditLog fetches audit log entries before the given timestamp.
// Returns up to ldMaxPageSize entries per call.
func (c *LaunchDarklyClient) ListAuditLog(ctx context.Context, before time.Time, limit int) ([]models.LDAuditEntry, error) {
	if limit <= 0 || limit > ldMaxPageSize {
		limit = ldMaxPageSize
	}

	url := fmt.Sprintf("%s/auditlog?limit=%d&before=%d", c.baseURL, limit, before.UnixMilli())

	var response LDAuditLogResponse
	if err := c.doRequest(ctx, "GET", url, &response); err != nil {
		return nil, fmt.Errorf("ListAuditLog: %w", err)
	}

	return response.Items, nil
}

// GetFlagDetail fetches the full flag configuration for a specific flag.
func (c *LaunchDarklyClient) GetFlagDetail(ctx context.Context, projectKey, flagKey string) (models.LDFlagDetail, error) {
	url := fmt.Sprintf("%s/flags/%s/%s", c.baseURL, projectKey, flagKey)

	var flag models.LDFlagDetail
	if err := c.doRequest(ctx, "GET", url, &flag); err != nil {
		return models.LDFlagDetail{}, fmt.Errorf("GetFlagDetail %s/%s: %w", projectKey, flagKey, err)
	}

	return flag, nil
}

func (c *LaunchDarklyClient) doRequest(ctx context.Context, method, url string, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	// Handle rate limiting - Temporal will retry with backoff
	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := resp.Header.Get("Retry-After")
		waitSeconds := ldDefaultRetryWait
		if retryAfter != "" {
			if parsed, parseErr := strconv.Atoi(retryAfter); parseErr == nil {
				waitSeconds = parsed
			}
		}
		return &RateLimitError{WaitSeconds: waitSeconds}
	}

	// Non-retryable auth errors
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, ldMaxErrorBodySize))
		return &InvalidAPIKeyError{StatusCode: resp.StatusCode, Message: string(body)}
	}

	// Not found - caller should handle gracefully
	if resp.StatusCode == http.StatusNotFound {
		return &NotFoundError{URL: url}
	}

	// Server errors - retryable via Temporal
	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, ldMaxErrorBodySize))
		return fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, ldMaxErrorBodySize))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// RateLimitError indicates the API rate limit was exceeded.
// Temporal should retry after WaitSeconds.
type RateLimitError struct {
	WaitSeconds int
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited, retry after %ds", e.WaitSeconds)
}

// InvalidAPIKeyError indicates authentication failed.
// This error should NOT be retried - the API key needs to be fixed.
type InvalidAPIKeyError struct {
	StatusCode int
	Message    string
}

func (e *InvalidAPIKeyError) Error() string {
	return fmt.Sprintf("invalid API key: %d %s", e.StatusCode, e.Message)
}

// NotFoundError indicates the requested resource does not exist.
// Callers should handle this gracefully (e.g., skip the flag).
type NotFoundError struct {
	URL string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("not found: %s", e.URL)
}
