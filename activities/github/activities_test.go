package github

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

func TestInstallationTokenFor(t *testing.T) {
	orgID := uuid.New()
	integrationKey := uuid.New()
	repo := &stubRepository{
		integrations: map[uuid.UUID]models.Integration{
			integrationKey: {
				Key:    integrationKey,
				OrgID:  orgID,
				Source: models.SourceGitHub,
				Config: models.IntegrationConfig{
					InstallationID: 12345,
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(installationTokenResponse{
			Token:     "installation-token",
			ExpiresAt: time.Date(2026, 4, 29, 13, 0, 0, 0, time.UTC),
		})
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	acts := New(client, repo)

	token, err := acts.installationTokenFor(context.Background(), orgID, integrationKey)
	if err != nil {
		t.Fatalf("installationTokenFor failed: %v", err)
	}
	if token.Token != "installation-token" {
		t.Fatalf("expected installation token, got %q", token.Token)
	}
}

func TestInstallationTokenFor_RequiresClient(t *testing.T) {
	acts := New(nil, &stubRepository{})
	if _, err := acts.installationTokenFor(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestInstallationTokenFor_RequiresInstallationID(t *testing.T) {
	orgID := uuid.New()
	integrationKey := uuid.New()
	repo := &stubRepository{
		integrations: map[uuid.UUID]models.Integration{
			integrationKey: {
				Key:    integrationKey,
				OrgID:  orgID,
				Source: models.SourceGitHub,
			},
		},
	}
	acts := New(mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t)), repo)

	if _, err := acts.installationTokenFor(context.Background(), orgID, integrationKey); err == nil {
		t.Fatal("expected error for missing installation_id")
	}
}

type stubRepository struct {
	integrations map[uuid.UUID]models.Integration
}

func (r *stubRepository) GetIntegration(_ context.Context, _ uuid.UUID, key uuid.UUID) (models.Integration, error) {
	return r.integrations[key], nil
}
