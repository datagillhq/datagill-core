package launchdarkly

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
	"github.com/datagill/datagill-core/shared"
)

func TestNewClient_EmptyAPIKey(t *testing.T) {
	_, err := NewClient("")
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
		_ = json.NewEncoder(w).Encode(auditLogResponse{
			Items: []models.LDAuditEntry{
				{ID: "entry-1", Kind: "flag", Name: "test-flag"},
				{ID: "entry-2", Kind: "flag", Name: "another-flag"},
			},
		})
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
	entries, nextURL, err := client.ListAuditLog(context.Background(), time.Now(), time.Time{}, 20)
	if err != nil {
		t.Fatalf("ListAuditLog failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ID != "entry-1" {
		t.Errorf("expected first entry ID 'entry-1', got %q", entries[0].ID)
	}
	if nextURL != "" {
		t.Errorf("expected empty nextURL when _links.next is absent, got %q", nextURL)
	}
}

func TestListAuditLog_QueryParams(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(auditLogResponse{})
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
	before := time.Date(2026, 3, 26, 10, 0, 0, 0, time.UTC)
	after := time.Date(2026, 3, 26, 9, 0, 0, 0, time.UTC)
	_, _, _ = client.ListAuditLog(context.Background(), before, after, 20)

	if gotQuery.Get("before") != strconv.FormatInt(before.UnixMilli(), 10) {
		t.Errorf("expected before=%d, got %s", before.UnixMilli(), gotQuery.Get("before"))
	}
	if gotQuery.Get("after") != strconv.FormatInt(after.UnixMilli(), 10) {
		t.Errorf("expected after=%d, got %s", after.UnixMilli(), gotQuery.Get("after"))
	}
}

func TestListAuditLog_ZeroTimesOmitted(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(auditLogResponse{})
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
	_, _, _ = client.ListAuditLog(context.Background(), time.Time{}, time.Time{}, 20)

	if gotQuery.Get("before") != "" {
		t.Errorf("expected no before param, got %s", gotQuery.Get("before"))
	}
	if gotQuery.Get("after") != "" {
		t.Errorf("expected no after param, got %s", gotQuery.Get("after"))
	}
}

func TestListAuditLog_NextURL(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := auditLogResponse{Items: []models.LDAuditEntry{{ID: "entry-" + strconv.Itoa(callCount)}}}
		if callCount == 1 {
			resp.Links.Next = &struct {
				Href string `json:"href"`
			}{Href: "http://" + r.Host + "/auditlog?limit=20&before=1000"}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))

	entries, nextURL, err := client.ListAuditLog(context.Background(), time.Now(), time.Time{}, 20)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "entry-1" {
		t.Errorf("expected entry-1, got %v", entries)
	}
	if nextURL == "" {
		t.Fatal("expected nextURL to be set")
	}

	entries, nextURL, err = client.ListAuditLogPage(context.Background(), nextURL)
	if err != nil {
		t.Fatalf("ListAuditLogPage failed: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "entry-2" {
		t.Errorf("expected entry-2, got %v", entries)
	}
	if nextURL != "" {
		t.Errorf("expected empty nextURL on last page, got %q", nextURL)
	}
}

func TestListAuditLog_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
	_, _, err := client.ListAuditLog(context.Background(), time.Now(), time.Time{}, 20)

	var svcErr *shared.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected ServiceError, got %T: %v", err, err)
	}
}

func TestListAuditLog_RateLimit_NoRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
	_, _, err := client.ListAuditLog(context.Background(), time.Now(), time.Time{}, 20)

	var svcErr *shared.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected ServiceError, got %T: %v", err, err)
	}
}

func TestListAuditLog_InvalidAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Invalid API key"))
	}))
	defer server.Close()

	client := mustNewClient(t, "bad-key", WithBaseURL(server.URL))
	_, _, err := client.ListAuditLog(context.Background(), time.Now(), time.Time{}, 20)

	var nonRetryableErr *shared.NonRetryableServiceError
	if !errors.As(err, &nonRetryableErr) {
		t.Fatalf("expected NonRetryableServiceError, got %T: %v", err, err)
	}
}

func TestListAuditLog_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(auditLogResponse{})
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := client.ListAuditLog(ctx, time.Now(), time.Time{}, 20)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestListAuditLog_LimitClamping(t *testing.T) {
	var gotLimit int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
		_ = json.NewEncoder(w).Encode(auditLogResponse{})
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))

	_, _, _ = client.ListAuditLog(context.Background(), time.Now(), time.Time{}, 0)
	if gotLimit != maxPageSize {
		t.Errorf("expected limit clamped to %d, got %d", maxPageSize, gotLimit)
	}

	_, _, _ = client.ListAuditLog(context.Background(), time.Now(), time.Time{}, 100)
	if gotLimit != maxPageSize {
		t.Errorf("expected limit clamped to %d, got %d", maxPageSize, gotLimit)
	}
}

func TestGetFlagDetail_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flags/my-project/my-flag" {
			t.Errorf("expected path /flags/my-project/my-flag, got %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(models.LDFlagDetail{Key: "my-flag", Name: "My Flag", Kind: "boolean"})
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
	_, err := client.GetFlagDetail(context.Background(), "my-project", "nonexistent-flag")

	var nonRetryableErr *shared.NonRetryableServiceError
	if !errors.As(err, &nonRetryableErr) {
		t.Fatalf("expected NonRetryableServiceError, got %T: %v", err, err)
	}
}

func TestGetFlagDetail_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal server error"))
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
	_, err := client.GetFlagDetail(context.Background(), "my-project", "my-flag")
	if err == nil {
		t.Fatal("expected error for server error response")
	}

	var nonRetryableErr *shared.NonRetryableServiceError
	if errors.As(err, &nonRetryableErr) {
		t.Errorf("expected ServiceError for 500, got NonRetryableServiceError: %v", err)
	}
}

func TestGetFlagDetail_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	client := mustNewClient(t, "test-api-key", WithBaseURL(server.URL))
	_, err := client.GetFlagDetail(context.Background(), "my-project", "my-flag")
	if err == nil {
		t.Fatal("expected error for malformed JSON response")
	}
}
