package launchdarkly

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

func TestListAuditLogs_Success(t *testing.T) {
	entries := []models.LDAuditEntry{
		{ID: "entry-1", Kind: "flag", Name: "test-flag", Date: time.Now().UnixMilli()},
		{ID: "entry-2", Kind: "flag", Name: "another-flag", Date: time.Now().Add(-time.Hour).UnixMilli()},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(auditLogResponse{Items: entries})
	}))
	defer server.Close()

	acts := New(mustNewClient(t, "test-key", WithBaseURL(server.URL)), newMockRepository())
	repo := acts.repo.(*mockRepository)

	out, err := acts.ListAuditLogs(context.Background(), ListAuditLogsInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		Before:         time.Now(),
		Limit:          20,
	})
	if err != nil {
		t.Fatalf("ListAuditLogs failed: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(out.Entries))
	}
	if len(repo.sourceRecords) != 2 {
		t.Errorf("expected 2 source records in repo, got %d", len(repo.sourceRecords))
	}
}

func TestListAuditLogs_HasMore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := auditLogResponse{
			Items: []models.LDAuditEntry{{ID: "entry-1", Kind: "flag", Date: time.Now().UnixMilli()}},
		}
		resp.Links.Next = &struct {
			Href string `json:"href"`
		}{Href: "http://" + r.Host + "/auditlog?limit=20&before=1000"}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	acts := New(mustNewClient(t, "test-key", WithBaseURL(server.URL)), newMockRepository())

	out, err := acts.ListAuditLogs(context.Background(), ListAuditLogsInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		Before:         time.Now(),
		Limit:          20,
	})
	if err != nil {
		t.Fatalf("ListAuditLogs failed: %v", err)
	}
	if !out.HasMore {
		t.Error("expected HasMore=true when _links.next is present")
	}
	if out.NextURL == "" {
		t.Error("expected NextURL to be set when _links.next is present")
	}
}

func TestScrollAuditLogs_SinglePage(t *testing.T) {
	entries := []models.LDAuditEntry{
		{ID: "entry-1", Kind: "flag", Date: time.Now().UnixMilli()},
		{ID: "entry-2", Kind: "flag", Date: time.Now().Add(-time.Hour).UnixMilli()},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(auditLogResponse{Items: entries})
	}))
	defer server.Close()

	acts := New(mustNewClient(t, "test-key", WithBaseURL(server.URL)), newMockRepository())
	out, err := acts.ScrollAuditLogs(context.Background(), ScrollAuditLogsInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
	})
	if err != nil {
		t.Fatalf("ScrollAuditLogs failed: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(out.Entries))
	}
}

func TestScrollAuditLogs_MultiPage(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := auditLogResponse{
			Items: []models.LDAuditEntry{
				{ID: "entry-" + strconv.Itoa(callCount), Kind: "flag", Date: time.Now().UnixMilli()},
			},
		}
		if callCount < 3 {
			resp.Links.Next = &struct {
				Href string `json:"href"`
			}{Href: "http://" + r.Host + "/auditlog?before=" + strconv.Itoa(callCount)}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	acts := New(mustNewClient(t, "test-key", WithBaseURL(server.URL)), newMockRepository())
	out, err := acts.ScrollAuditLogs(context.Background(), ScrollAuditLogsInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
	})
	if err != nil {
		t.Fatalf("ScrollAuditLogs failed: %v", err)
	}
	if len(out.Entries) != 3 {
		t.Errorf("expected 3 entries across 3 pages, got %d", len(out.Entries))
	}
	if callCount != 3 {
		t.Errorf("expected 3 HTTP calls, got %d", callCount)
	}
}

func TestListAuditLogs_Pagination(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := auditLogResponse{
			Items: []models.LDAuditEntry{
				{ID: "entry-" + strconv.Itoa(callCount), Kind: "flag", Date: time.Now().UnixMilli()},
			},
		}
		if callCount == 1 {
			resp.Links.Next = &struct {
				Href string `json:"href"`
			}{Href: "http://" + r.Host + "/auditlog?limit=20&before=1000"}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	repo := newMockRepository()
	acts := New(mustNewClient(t, "test-key", WithBaseURL(server.URL)), repo)
	base := ListAuditLogsInput{IntegrationKey: uuid.New(), OrgID: uuid.New(), Before: time.Now(), Limit: 20}

	out1, err := acts.ListAuditLogs(context.Background(), base)
	if err != nil {
		t.Fatalf("first page failed: %v", err)
	}
	if !out1.HasMore || out1.NextURL == "" {
		t.Fatal("expected HasMore=true and NextURL set after first page")
	}

	out2, err := acts.ListAuditLogs(context.Background(), ListAuditLogsInput{
		IntegrationKey: base.IntegrationKey,
		OrgID:          base.OrgID,
		NextURL:        out1.NextURL,
	})
	if err != nil {
		t.Fatalf("second page failed: %v", err)
	}
	if out2.HasMore {
		t.Error("expected HasMore=false on last page")
	}
	if len(repo.sourceRecords) != 2 {
		t.Errorf("expected 2 total source records, got %d", len(repo.sourceRecords))
	}
}
