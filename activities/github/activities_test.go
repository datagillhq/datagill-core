package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/shared"
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

	token, err := acts.getInstallationToken(context.Background(), orgID, integrationKey)
	if err != nil {
		t.Fatalf("getInstallationToken failed: %v", err)
	}
	if token.Token != "installation-token" {
		t.Fatalf("expected installation token, got %q", token.Token)
	}
}

func TestInstallationTokenFor_RequiresClient(t *testing.T) {
	acts := New(nil, &stubRepository{})
	if _, err := acts.getInstallationToken(context.Background(), uuid.New(), uuid.New()); err == nil {
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

	if _, err := acts.getInstallationToken(context.Background(), orgID, integrationKey); err == nil {
		t.Fatal("expected error for missing installation_id")
	}
}

func TestInstallationTokenFor_RequiresGitHubSource(t *testing.T) {
	orgID := uuid.New()
	integrationKey := uuid.New()
	repo := &stubRepository{
		integrations: map[uuid.UUID]models.Integration{
			integrationKey: {
				Key:    integrationKey,
				OrgID:  orgID,
				Source: models.SourceLaunchDarkly,
				Config: models.IntegrationConfig{
					InstallationID: 12345,
				},
			},
		},
	}
	acts := New(mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t)), repo)

	if _, err := acts.getInstallationToken(context.Background(), orgID, integrationKey); err == nil {
		t.Fatal("expected error for non-github source")
	}
}

func TestInstallationTokenFor_MapsNotFoundToNonRetryable(t *testing.T) {
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("installation not found"))
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	acts := New(client, repo)

	_, err := acts.getInstallationToken(context.Background(), orgID, integrationKey)
	var nonRetryable *shared.NonRetryableServiceError
	if !errors.As(err, &nonRetryable) {
		t.Fatalf("expected NonRetryableServiceError, got %T: %v", err, err)
	}
}

type stubRepository struct {
	integrations  map[uuid.UUID]models.Integration
	sourceRecords []models.SourceRecord
}

func (r *stubRepository) GetIntegration(_ context.Context, _ uuid.UUID, key uuid.UUID) (models.Integration, error) {
	return r.integrations[key], nil
}

func (r *stubRepository) SaveSourceRecord(_ context.Context, rec models.SourceRecord) error {
	r.sourceRecords = append(r.sourceRecords, rec)
	return nil
}

func TestSearchPullRequests(t *testing.T) {
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
					AccountLogin:   "acme",
					AccountType:    "Organization",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/12345/access_tokens":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(installationTokenResponse{
				Token:     "installation-token",
				ExpiresAt: time.Date(2026, 4, 29, 13, 0, 0, 0, time.UTC),
			})
		case "/search/issues":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(models.GitHubSearchResponse{
				TotalCount: 200,
				Items: []models.GitHubSearchResultItem{
					{
						ID:            987654321,
						Number:        42,
						RepositoryURL: "https://api.github.com/repos/acme/backend",
						PullRequest:   &models.GitHubPullRequestLinks{URL: "https://api.github.com/repos/acme/backend/pulls/42"},
					},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	acts := New(client, repo)

	out, err := acts.SearchPullRequests(context.Background(), SearchPullRequestsInput{
		IntegrationKey: integrationKey,
		OrgID:          orgID,
		From:           time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC),
		Page:           1,
		PerPage:        1,
	})
	if err != nil {
		t.Fatalf("SearchPullRequests failed: %v", err)
	}
	if len(out.Refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(out.Refs))
	}
	if out.Refs[0].Owner != "acme" || out.Refs[0].Repo != "backend" || out.Refs[0].Number != 42 {
		t.Fatalf("unexpected ref: %+v", out.Refs[0])
	}
	if out.NextPage != 2 {
		t.Fatalf("expected next page 2, got %d", out.NextPage)
	}
	if len(repo.sourceRecords) != 1 {
		t.Fatalf("expected 1 source record, got %d", len(repo.sourceRecords))
	}
	if repo.sourceRecords[0].SourceType != models.SourceTypePRSearch {
		t.Fatalf("expected source type pr_search, got %q", repo.sourceRecords[0].SourceType)
	}
}

func TestSearchPullRequests_RequiresAccountLogin(t *testing.T) {
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
		if r.URL.Path == "/app/installations/12345/access_tokens" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(installationTokenResponse{Token: "installation-token"})
			return
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
	}))
	defer server.Close()

	acts := New(mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL)), repo)
	if _, err := acts.SearchPullRequests(context.Background(), SearchPullRequestsInput{
		IntegrationKey: integrationKey,
		OrgID:          orgID,
		From:           time.Now(),
		To:             time.Now().Add(time.Hour),
	}); err == nil {
		t.Fatal("expected error for missing account_login")
	}
}

func TestSearchPullRequests_RequiresAccountType(t *testing.T) {
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
					AccountLogin:   "acme",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations/12345/access_tokens" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(installationTokenResponse{Token: "installation-token"})
			return
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
	}))
	defer server.Close()

	acts := New(mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL)), repo)
	_, err := acts.SearchPullRequests(context.Background(), SearchPullRequestsInput{
		IntegrationKey: integrationKey,
		OrgID:          orgID,
		From:           time.Now(),
		To:             time.Now().Add(time.Hour),
	})
	var nonRetryable *shared.NonRetryableServiceError
	if !errors.As(err, &nonRetryable) {
		t.Fatalf("expected NonRetryableServiceError, got %T: %v", err, err)
	}
}

func TestSearchPullRequests_MapsBadRepositoryURLToNonRetryable(t *testing.T) {
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
					AccountLogin:   "acme",
					AccountType:    "Organization",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/12345/access_tokens":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(installationTokenResponse{Token: "installation-token"})
		case "/search/issues":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(models.GitHubSearchResponse{
				Items: []models.GitHubSearchResultItem{
					{
						ID:            987654321,
						Number:        42,
						RepositoryURL: "https://example.com/repos/acme/backend",
						PullRequest:   &models.GitHubPullRequestLinks{URL: "https://api.github.com/repos/acme/backend/pulls/42"},
					},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	acts := New(mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL)), repo)
	_, err := acts.SearchPullRequests(context.Background(), SearchPullRequestsInput{
		IntegrationKey: integrationKey,
		OrgID:          orgID,
		From:           time.Now(),
		To:             time.Now().Add(time.Hour),
	})
	var nonRetryable *shared.NonRetryableServiceError
	if !errors.As(err, &nonRetryable) {
		t.Fatalf("expected NonRetryableServiceError, got %T: %v", err, err)
	}
}
