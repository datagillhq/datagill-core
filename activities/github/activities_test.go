package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

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
	pullRequests  []models.PullRequest
}

func (r *stubRepository) GetIntegration(_ context.Context, _ uuid.UUID, key uuid.UUID) (models.Integration, error) {
	return r.integrations[key], nil
}

func (r *stubRepository) SaveSourceRecord(_ context.Context, rec models.SourceRecord) error {
	r.sourceRecords = append(r.sourceRecords, rec)
	return nil
}

func (r *stubRepository) SavePullRequest(_ context.Context, pr models.PullRequest) error {
	r.pullRequests = append(r.pullRequests, pr)
	return nil
}

func (r *stubRepository) GetPullRequestByKey(_ context.Context, _ uuid.UUID) (models.PullRequest, error) {
	return models.PullRequest{}, nil
}

func (r *stubRepository) GetFeatureFlagsBySourceKeys(_ context.Context, _ uuid.UUID, _ []string) ([]models.FeatureFlag, error) {
	return nil, nil
}

func (r *stubRepository) ListFeatureFlagSourceKeys(_ context.Context, _ uuid.UUID) ([]string, error) {
	return nil, nil
}

func (r *stubRepository) ListPendingEnrichments(_ context.Context, _ uuid.UUID, _ int) ([]models.PullRequestEnrichment, error) {
	return nil, nil
}

func (r *stubRepository) ClaimEnrichment(_ context.Context, _ uuid.UUID) error { return nil }

func (r *stubRepository) SaveEnrichmentResult(_ context.Context, _ uuid.UUID, _ models.PullRequestEnrichmentData) error {
	return nil
}

func (r *stubRepository) MarkEnrichmentFailed(_ context.Context, _ uuid.UUID) error { return nil }

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
						Labels:        []models.GitHubLabelSummary{{Name: "feature"}, {Name: "backend"}},
						Assignees:     []models.GitHubUserSummary{{Login: "mira", ID: 2001}},
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
	if len(out.Refs[0].Labels) != 2 || out.Refs[0].Labels[0] != "feature" || out.Refs[0].Labels[1] != "backend" {
		t.Fatalf("unexpected labels: %+v", out.Refs[0].Labels)
	}
	if len(out.Refs[0].Assignees) != 1 || out.Refs[0].Assignees[0].Login != "mira" || out.Refs[0].Assignees[0].ID != 2001 {
		t.Fatalf("unexpected assignees: %+v", out.Refs[0].Assignees)
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

func TestGetPullRequestDetail(t *testing.T) {
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
	createdAt := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	mergedAt := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/12345/access_tokens":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(installationTokenResponse{Token: "installation-token"})
		case "/repos/acme/backend/pulls/42":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{
				"id": 123456789,
				"number": 42,
				"title": "Ship checkout",
				"html_url": "https://github.com/acme/backend/pull/42",
				"diff_url": "https://github.com/acme/backend/pull/42.diff",
				"created_at": %q,
				"merged_at": %q,
				"user": {"login": "ariel", "id": 1001},
				"base": {"ref": "main", "repo": {"id": 9001, "name": "backend", "owner": {"login": "acme"}}},
				"head": {"ref": "checkout-v2"},
				"unknown_future_field": {"must": "survive"}
			}`, createdAt.Format(time.RFC3339), mergedAt.Format(time.RFC3339))))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	acts := New(mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL)), repo)
	out, err := acts.GetPullRequestDetail(context.Background(), GetPullRequestDetailInput{
		IntegrationKey: integrationKey,
		OrgID:          orgID,
		Ref: PullRequestRef{
			Owner:  "acme",
			Repo:   "backend",
			Number: 42,
		},
	})
	if err != nil {
		t.Fatalf("GetPullRequestDetail failed: %v", err)
	}
	if out.Detail.ID != 123456789 {
		t.Fatalf("expected detail id 123456789, got %d", out.Detail.ID)
	}
	expectedKey := models.ComputeSourceRecordKey(orgID, models.SourceGitHub, models.SourceTypePRDetail, "123456789")
	if out.SourceRecordKey != expectedKey {
		t.Fatalf("expected source record key %s, got %s", expectedKey, out.SourceRecordKey)
	}
	if len(repo.sourceRecords) != 1 {
		t.Fatalf("expected 1 source record, got %d", len(repo.sourceRecords))
	}
	if repo.sourceRecords[0].SourceType != models.SourceTypePRDetail {
		t.Fatalf("expected source type pr_detail, got %q", repo.sourceRecords[0].SourceType)
	}
	if !strings.Contains(string(repo.sourceRecords[0].Data), `"unknown_future_field"`) {
		t.Fatalf("expected raw source record to preserve unknown fields, got %s", string(repo.sourceRecords[0].Data))
	}
}

func TestGetPullRequestDetail_RequiresValidRef(t *testing.T) {
	acts := New(mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t)), &stubRepository{})
	tests := []struct {
		name string
		ref  PullRequestRef
	}{
		{name: "owner", ref: PullRequestRef{Repo: "backend", Number: 42}},
		{name: "repo", ref: PullRequestRef{Owner: "acme", Number: 42}},
		{name: "number", ref: PullRequestRef{Owner: "acme", Repo: "backend"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := acts.GetPullRequestDetail(context.Background(), GetPullRequestDetailInput{
				IntegrationKey: uuid.New(),
				OrgID:          uuid.New(),
				Ref:            tt.ref,
			})
			var nonRetryable *shared.NonRetryableServiceError
			if !errors.As(err, &nonRetryable) {
				t.Fatalf("expected NonRetryableServiceError, got %T: %v", err, err)
			}
		})
	}
}

func TestNormalizePullRequest(t *testing.T) {
	orgID := uuid.New()
	integrationKey := uuid.New()
	sourceRecordKey := uuid.New()
	createdAt := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	mergedAt := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)

	acts := New(nil, &stubRepository{})
	pr, err := acts.NormalizePullRequest(context.Background(), NormalizePullRequestInput{
		IntegrationKey:  integrationKey,
		OrgID:           orgID,
		SourceRecordKey: sourceRecordKey,
		Ref: PullRequestRef{
			Labels:    []string{"feature", "backend"},
			Assignees: []models.PullRequestAssignee{{Login: "mira", ID: 2001}},
		},
		Detail: models.GitHubPullRequestDetail{
			ID:             123456789,
			Number:         42,
			Title:          "Ship checkout",
			Body:           "Enables checkout v2.",
			HTMLURL:        "https://github.com/acme/backend/pull/42",
			DiffURL:        "https://github.com/acme/backend/pull/42.diff",
			MergeCommitSHA: "abc123",
			Additions:      10,
			Deletions:      2,
			ChangedFiles:   3,
			CreatedAt:      createdAt,
			MergedAt:       &mergedAt,
			User:           models.GitHubUserSummary{Login: "ariel", ID: 1001},
			Base: models.GitHubPullRequestBranch{
				Ref: "main",
				Repo: models.GitHubRepositorySummary{
					ID:    9001,
					Name:  "backend",
					Owner: models.GitHubUserSummary{Login: "acme"},
				},
			},
			Head: models.GitHubPullRequestBranch{Ref: "checkout-v2"},
		},
	})
	if err != nil {
		t.Fatalf("NormalizePullRequest failed: %v", err)
	}
	if pr.Key != models.ComputePullRequestKey(orgID, "123456789") {
		t.Fatalf("unexpected pull request key %s", pr.Key)
	}
	if pr.SourceRecordKey != sourceRecordKey || pr.IntegrationID != integrationKey || pr.OrgID != orgID {
		t.Fatalf("unexpected identity fields: %+v", pr)
	}
	if pr.State != models.PullRequestStateMerged {
		t.Fatalf("expected merged state, got %q", pr.State)
	}
	if pr.Data.Source.RepositoryID != "9001" || pr.Data.Source.Provider != models.SourceGitHub {
		t.Fatalf("unexpected source data: %+v", pr.Data.Source)
	}
	if pr.Data.Details.Repository.Owner != "acme" || pr.Data.Details.Repository.Name != "backend" {
		t.Fatalf("unexpected repository: %+v", pr.Data.Details.Repository)
	}
	if pr.Data.Details.Diff.SHA != "abc123" || pr.Data.Details.Diff.Branches.Source != "checkout-v2" || pr.Data.Details.Diff.Branches.Target != "main" {
		t.Fatalf("unexpected diff: %+v", pr.Data.Details.Diff)
	}
	if pr.Data.Details.Diff.Stats.Additions != 10 || pr.Data.Details.Diff.Stats.Deletions != 2 || pr.Data.Details.Diff.Stats.ChangedFiles != 3 {
		t.Fatalf("unexpected stats: %+v", pr.Data.Details.Diff.Stats)
	}
	if len(pr.Data.Details.Labels) != 2 || pr.Data.Details.Labels[0] != "feature" || pr.Data.Details.Labels[1] != "backend" {
		t.Fatalf("unexpected labels: %+v", pr.Data.Details.Labels)
	}
	if len(pr.Data.Details.Assignees) != 1 || pr.Data.Details.Assignees[0].Login != "mira" || pr.Data.Details.Assignees[0].ID != 2001 {
		t.Fatalf("unexpected assignees: %+v", pr.Data.Details.Assignees)
	}
}

func TestNormalizePullRequest_UsesFallbackRepository(t *testing.T) {
	acts := New(nil, &stubRepository{})
	pr, err := acts.NormalizePullRequest(context.Background(), NormalizePullRequestInput{
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		SourceRecordKey: uuid.New(),
		Ref: PullRequestRef{
			Owner: "acme",
			Repo:  "backend",
		},
		Detail: models.GitHubPullRequestDetail{
			ID:        123456789,
			CreatedAt: time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("NormalizePullRequest failed: %v", err)
	}
	if pr.Data.Details.Repository.Owner != "acme" || pr.Data.Details.Repository.Name != "backend" {
		t.Fatalf("unexpected fallback repository: %+v", pr.Data.Details.Repository)
	}
}

func TestToServiceError_RateLimitedSetsNextRetryDelay(t *testing.T) {
	err := toServiceError(&TooManyRequestsError{RetryAfter: 120})

	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *temporal.ApplicationError, got %T", err)
	}
	if appErr.NextRetryDelay() != 120*time.Second {
		t.Fatalf("expected 120s retry delay, got %v", appErr.NextRetryDelay())
	}
	if appErr.NonRetryable() {
		t.Fatal("rate limit error should be retryable")
	}
}

func TestToServiceError_RateLimitedUsesDefaultWhenRetryAfterIsZero(t *testing.T) {
	err := toServiceError(&TooManyRequestsError{RetryAfter: 0})

	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *temporal.ApplicationError, got %T", err)
	}
	if appErr.NextRetryDelay() != defaultRateLimitRetryAfter {
		t.Fatalf("expected default retry delay %v, got %v", defaultRateLimitRetryAfter, appErr.NextRetryDelay())
	}
}

func TestSavePullRequest(t *testing.T) {
	repo := &stubRepository{}
	acts := New(nil, repo)
	pr := models.PullRequest{Key: uuid.New()}

	if err := acts.SavePullRequest(context.Background(), pr); err != nil {
		t.Fatalf("SavePullRequest failed: %v", err)
	}
	if len(repo.pullRequests) != 1 || repo.pullRequests[0].Key != pr.Key {
		t.Fatalf("expected saved pull request, got %+v", repo.pullRequests)
	}
}
