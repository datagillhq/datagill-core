package activities

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/datagill/datagill-core/models"
)

func TestNewLaunchDarklyClient_EmptyAPIKey(t *testing.T) {
	_, err := NewLaunchDarklyClient("")
	if err == nil {
		t.Error("expected error for empty API key")
	}
}

func TestListAuditLog_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "test-api-key" {
			t.Errorf("expected Authorization header 'test-api-key', got %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/auditlog" {
			t.Errorf("expected path /auditlog, got %s", r.URL.Path)
		}

		response := LDAuditLogResponse{
			Items: []models.LDAuditEntry{
				{ID: "entry-1", Kind: "flag", Name: "test-flag"},
				{ID: "entry-2", Kind: "flag", Name: "another-flag"},
			},
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-api-key", WithBaseURL(server.URL))
	entries, err := client.ListAuditLog(context.Background(), time.Now(), 20)
	if err != nil {
		t.Fatalf("ListAuditLog failed: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ID != "entry-1" {
		t.Errorf("expected first entry ID 'entry-1', got %q", entries[0].ID)
	}
}

func TestListAuditLog_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-api-key", WithBaseURL(server.URL))
	_, err := client.ListAuditLog(context.Background(), time.Now(), 20)

	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected RateLimitError, got %T: %v", err, err)
	}
	if rateLimitErr.WaitSeconds != 30 {
		t.Errorf("expected WaitSeconds 30, got %d", rateLimitErr.WaitSeconds)
	}
}

func TestListAuditLog_RateLimit_NoRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-api-key", WithBaseURL(server.URL))
	_, err := client.ListAuditLog(context.Background(), time.Now(), 20)

	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected RateLimitError, got %T: %v", err, err)
	}
	if rateLimitErr.WaitSeconds != ldDefaultRetryWait {
		t.Errorf("expected default WaitSeconds %d, got %d", ldDefaultRetryWait, rateLimitErr.WaitSeconds)
	}
}

func TestListAuditLog_InvalidAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Invalid API key"))
	}))
	defer server.Close()

	client := mustCreateClient(t, "bad-key", WithBaseURL(server.URL))
	_, err := client.ListAuditLog(context.Background(), time.Now(), 20)

	var apiKeyErr *InvalidAPIKeyError
	if !errors.As(err, &apiKeyErr) {
		t.Fatalf("expected InvalidAPIKeyError, got %T: %v", err, err)
	}
	if apiKeyErr.StatusCode != 401 {
		t.Errorf("expected StatusCode 401, got %d", apiKeyErr.StatusCode)
	}
}

func TestGetFlagDetail_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flags/my-project/my-flag" {
			t.Errorf("expected path /flags/my-project/my-flag, got %s", r.URL.Path)
		}

		flag := models.LDFlagDetail{
			Key:  "my-flag",
			Name: "My Flag",
			Kind: "boolean",
		}
		_ = json.NewEncoder(w).Encode(flag)
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-api-key", WithBaseURL(server.URL))
	flag, err := client.GetFlagDetail(context.Background(), "my-project", "my-flag")
	if err != nil {
		t.Fatalf("GetFlagDetail failed: %v", err)
	}

	if flag.Key != "my-flag" {
		t.Errorf("expected flag key 'my-flag', got %q", flag.Key)
	}
	if flag.Name != "My Flag" {
		t.Errorf("expected flag name 'My Flag', got %q", flag.Name)
	}
}

func TestGetFlagDetail_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-api-key", WithBaseURL(server.URL))
	_, err := client.GetFlagDetail(context.Background(), "my-project", "nonexistent-flag")

	var notFoundErr *NotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestGetFlagDetail_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal server error"))
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-api-key", WithBaseURL(server.URL))
	_, err := client.GetFlagDetail(context.Background(), "my-project", "my-flag")

	if err == nil {
		t.Fatal("expected error for server error response")
	}
	// Server errors should be generic errors (retryable by Temporal)
	var notFoundErr *NotFoundError
	var apiKeyErr *InvalidAPIKeyError
	if errors.As(err, &notFoundErr) || errors.As(err, &apiKeyErr) {
		t.Errorf("expected generic error for 500, got specialized error: %v", err)
	}
}

func TestGetFlagDetail_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-api-key", WithBaseURL(server.URL))
	_, err := client.GetFlagDetail(context.Background(), "my-project", "my-flag")

	if err == nil {
		t.Fatal("expected error for malformed JSON response")
	}
}

func TestListAuditLog_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(100 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(LDAuditLogResponse{})
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-api-key", WithBaseURL(server.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.ListAuditLog(ctx, time.Now(), 20)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestLimitClamping(t *testing.T) {
	requestedLimit := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedLimit = getLimitFromQuery(r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode(LDAuditLogResponse{})
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-api-key", WithBaseURL(server.URL))

	// Test zero limit gets clamped to max
	_, _ = client.ListAuditLog(context.Background(), time.Now(), 0)
	if requestedLimit != ldMaxPageSize {
		t.Errorf("expected limit to be clamped to %d, got %d", ldMaxPageSize, requestedLimit)
	}

	// Test over-max limit gets clamped
	_, _ = client.ListAuditLog(context.Background(), time.Now(), 100)
	if requestedLimit != ldMaxPageSize {
		t.Errorf("expected limit to be clamped to %d, got %d", ldMaxPageSize, requestedLimit)
	}
}

// Test helpers

func mustCreateClient(t *testing.T, apiKey string, opts ...LaunchDarklyClientOption) *LaunchDarklyClient {
	t.Helper()
	client, err := NewLaunchDarklyClient(apiKey, opts...)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return client
}

func getLimitFromQuery(rawQuery string) int {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return 0
	}
	limit, _ := strconv.Atoi(values.Get("limit"))
	return limit
}
