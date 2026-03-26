package launchdarkly

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

func TestGetFlagDetailActivity_Success(t *testing.T) {
	flag := models.LDFlagDetail{
		Key:          "my-flag",
		Name:         "My Flag",
		Kind:         "boolean",
		CreationDate: time.Now().UnixMilli(),
		Environments: map[string]models.LDFlagEnv{
			"production": {On: true},
			"staging":    {On: false},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(flag)
	}))
	defer server.Close()

	repo := newMockRepository()
	acts := New(mustNewClient(t, "test-key", WithBaseURL(server.URL)), repo)

	out, err := acts.GetFlagDetail(context.Background(), GetFlagDetailInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
		FlagKey:        "my-flag",
	})
	if err != nil {
		t.Fatalf("GetFlagDetail failed: %v", err)
	}
	if out.Flag.Key != "my-flag" {
		t.Errorf("expected flag key 'my-flag', got %q", out.Flag.Key)
	}
	if out.SourceRecordKey == uuid.Nil {
		t.Error("expected non-nil SourceRecordKey")
	}
	if len(repo.sourceRecords) != 1 {
		t.Errorf("expected 1 source record in repo, got %d", len(repo.sourceRecords))
	}
}

func TestGetFlagDetailActivity_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	acts := New(mustNewClient(t, "test-key", WithBaseURL(server.URL)), newMockRepository())

	_, err := acts.GetFlagDetail(context.Background(), GetFlagDetailInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
		FlagKey:        "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for not found flag")
	}
}
