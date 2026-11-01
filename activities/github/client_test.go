package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/datagill/datagill-core/models"
)

func TestNewClient_RequiresAppID(t *testing.T) {
	if _, err := NewClient("", testGitHubPrivateKeyPEM(t)); err == nil {
		t.Fatal("expected error for missing app id")
	}
}

func TestNewClient_RequiresPrivateKey(t *testing.T) {
	if _, err := NewClient("123456", nil); err == nil {
		t.Fatal("expected error for missing private key")
	}
}

func TestCreateInstallationToken_Success(t *testing.T) {
	privateKeyPEM := testGitHubPrivateKeyPEM(t)
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	var authHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/app/installations/12345/access_tokens" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(installationTokenResponse{
			Token:     "installation-token",
			ExpiresAt: now.Add(time.Hour),
		})
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", privateKeyPEM, WithBaseURL(server.URL), WithNow(func() time.Time { return now }))
	token, err := client.CreateInstallationToken(context.Background(), 12345)
	if err != nil {
		t.Fatalf("CreateInstallationToken failed: %v", err)
	}
	if token.Token != "installation-token" {
		t.Fatalf("expected installation token, got %q", token.Token)
	}
	if token.ExpiresAt != now.Add(time.Hour) {
		t.Fatalf("expected expires_at %s, got %s", now.Add(time.Hour), token.ExpiresAt)
	}
	assertAppJWT(t, authHeader, "123456", privateKeyPEM, now)
}

func TestCreateInstallationToken_RequiresInstallationID(t *testing.T) {
	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t))
	if _, err := client.CreateInstallationToken(context.Background(), 0); err == nil {
		t.Fatal("expected error for missing installation id")
	}
}

func TestCreateInstallationToken_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad credentials"))
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	_, err := client.CreateInstallationToken(context.Background(), 12345)
	var unauthorizedErr *UnauthorizedError
	if !errors.As(err, &unauthorizedErr) {
		t.Fatalf("expected UnauthorizedError, got %T: %v", err, err)
	}
	if unauthorizedErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorizedErr.StatusCode)
	}
}

func TestCreateInstallationToken_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("installation not found"))
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	_, err := client.CreateInstallationToken(context.Background(), 12345)
	var notFoundErr *NotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
	if notFoundErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", notFoundErr.StatusCode)
	}
}

func TestCreateInstallationToken_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	_, err := client.CreateInstallationToken(context.Background(), 12345)
	var rateLimitErr *TooManyRequestsError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected TooManyRequestsError, got %T: %v", err, err)
	}
	if rateLimitErr.RetryAfter != 30 {
		t.Fatalf("expected RetryAfter 30, got %d", rateLimitErr.RetryAfter)
	}
}

// GitHub signals rate limits with 403 as well as 429. Those must be retried after the
// wait, not treated as bad credentials.
func TestRateLimitFromResponse(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithNow(func() time.Time { return now }))

	tests := []struct {
		name     string
		status   int
		headers  map[string]string
		wantWait int // 0: not a rate limit
	}{
		{"429 with Retry-After", http.StatusTooManyRequests, map[string]string{"Retry-After": "30"}, 30},
		{"429 without headers", http.StatusTooManyRequests, nil, defaultRetryWait},
		{"403 secondary limit", http.StatusForbidden, map[string]string{"Retry-After": "90"}, 90},
		{
			"403 hourly quota used up waits until reset",
			http.StatusForbidden,
			map[string]string{"X-Ratelimit-Remaining": "0", "X-Ratelimit-Reset": strconv.FormatInt(now.Add(20*time.Minute).Unix(), 10)},
			1200,
		},
		{"403 quota used up, reset already passed", http.StatusForbidden, map[string]string{"X-Ratelimit-Remaining": "0", "X-Ratelimit-Reset": strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)}, defaultRetryWait},
		{"403 permissions error", http.StatusForbidden, map[string]string{"X-Ratelimit-Remaining": "4999"}, 0},
		{"401", http.StatusUnauthorized, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.status, Header: http.Header{}}
			for k, v := range tt.headers {
				resp.Header.Set(k, v)
			}

			got := client.rateLimitFromResponse(resp)
			if tt.wantWait == 0 {
				if got != nil {
					t.Fatalf("expected no rate limit, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected a rate limit")
			}
			if got.RetryAfter != tt.wantWait {
				t.Errorf("RetryAfter = %d, want %d", got.RetryAfter, tt.wantWait)
			}
		})
	}
}

func TestFetchRawDiff_ForbiddenRateLimitIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("You have exceeded a secondary rate limit"))
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	_, err := client.FetchRawDiff(context.Background(), "token", "acme", "backend", 42, "main", "abc123")

	var rateLimitErr *TooManyRequestsError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected TooManyRequestsError, got %T: %v", err, err)
	}
	if rateLimitErr.RetryAfter != 45 {
		t.Errorf("RetryAfter = %d, want 45", rateLimitErr.RetryAfter)
	}
}

func TestCreateInstallationToken_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(installationTokenResponse{Token: "late"})
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.CreateInstallationToken(ctx, 12345); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestSearchPullRequests_SuccessOrganization(t *testing.T) {
	var (
		authHeader string
		gotQuery   url.Values
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.GitHubSearchResponse{
			TotalCount: 1,
			Items: []models.GitHubSearchResultItem{
				{
					ID:            987654321,
					Number:        42,
					RepositoryURL: "https://api.github.com/repos/acme/backend",
					PullRequest:   &models.GitHubPullRequestLinks{URL: "https://api.github.com/repos/acme/backend/pulls/42"},
				},
			},
		})
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	from := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	resp, err := client.SearchPullRequests(context.Background(), "installation-token", "acme", "Organization", from, to, 2, 50)
	if err != nil {
		t.Fatalf("SearchPullRequests failed: %v", err)
	}
	if authHeader != "Bearer installation-token" {
		t.Fatalf("expected bearer token, got %q", authHeader)
	}
	if gotQuery.Get("page") != "2" || gotQuery.Get("per_page") != "50" {
		t.Fatalf("unexpected pagination query: %v", gotQuery)
	}
	expectedQ := "is:pull-request is:merged org:acme merged:2026-04-28T00:00:00Z..2026-04-29T00:00:00Z"
	if gotQuery.Get("q") != expectedQ {
		t.Fatalf("expected q %q, got %q", expectedQ, gotQuery.Get("q"))
	}
	if resp.TotalCount != 1 || len(resp.Items) != 1 || resp.Items[0].Number != 42 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestSearchPullRequests_SuccessUser(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.GitHubSearchResponse{})
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	from := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	if _, err := client.SearchPullRequests(context.Background(), "installation-token", "ariel", "User", from, to, 1, 100); err != nil {
		t.Fatalf("SearchPullRequests failed: %v", err)
	}
	expectedQ := "is:pull-request is:merged user:ariel merged:2026-04-28T00:00:00Z..2026-04-29T00:00:00Z"
	if gotQuery.Get("q") != expectedQ {
		t.Fatalf("expected q %q, got %q", expectedQ, gotQuery.Get("q"))
	}
}

func TestSearchPullRequests_ClampsPagination(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.GitHubSearchResponse{})
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	_, err := client.SearchPullRequests(context.Background(), "installation-token", "acme", "Organization", time.Now(), time.Now().Add(time.Hour), 0, 999)
	if err != nil {
		t.Fatalf("SearchPullRequests failed: %v", err)
	}
	if gotQuery.Get("page") != "1" {
		t.Fatalf("expected page=1, got %q", gotQuery.Get("page"))
	}
	if gotQuery.Get("per_page") != strconv.Itoa(searchMaxPerPage) {
		t.Fatalf("expected per_page=%d, got %q", searchMaxPerPage, gotQuery.Get("per_page"))
	}
}

func TestSearchPullRequests_RequiresSupportedAccountType(t *testing.T) {
	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t))
	_, err := client.SearchPullRequests(context.Background(), "installation-token", "acme", "Bot", time.Now(), time.Now().Add(time.Hour), 1, 100)
	if err == nil {
		t.Fatal("expected error for unsupported account type")
	}
}

func TestGetPullRequestDetail_Success(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/repos/acme/backend/pulls/42" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.GitHubPullRequestDetail{
			ID:       123456789,
			Number:   42,
			Title:    "Ship checkout",
			HTMLURL:  "https://github.com/acme/backend/pull/42",
			DiffURL:  "https://github.com/acme/backend/pull/42.diff",
			MergedAt: ptrTime(time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)),
			User:     models.GitHubUserSummary{Login: "ariel", ID: 1001},
			Base: models.GitHubPullRequestBranch{
				Ref: "main",
				Repo: models.GitHubRepositorySummary{
					ID:    9001,
					Name:  "backend",
					Owner: models.GitHubUserSummary{Login: "acme"},
				},
			},
			Head: models.GitHubPullRequestBranch{Ref: "checkout-v2"},
		})
	}))
	defer server.Close()

	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t), WithBaseURL(server.URL))
	resp, err := client.GetPullRequestDetail(context.Background(), "installation-token", "acme", "backend", 42)
	if err != nil {
		t.Fatalf("GetPullRequestDetail failed: %v", err)
	}
	if authHeader != "Bearer installation-token" {
		t.Fatalf("expected bearer token, got %q", authHeader)
	}
	if resp.Detail.ID != 123456789 || resp.Detail.Number != 42 || resp.Detail.Title != "Ship checkout" {
		t.Fatalf("unexpected detail: %+v", resp.Detail)
	}
	if len(resp.Raw) == 0 {
		t.Fatal("expected raw response bytes")
	}
}

func TestGetPullRequestDetail_RequiresValidInput(t *testing.T) {
	client := mustNewClient(t, "123456", testGitHubPrivateKeyPEM(t))
	tests := []struct {
		name   string
		token  string
		owner  string
		repo   string
		number int64
	}{
		{name: "token", owner: "acme", repo: "backend", number: 42},
		{name: "owner", token: "token", repo: "backend", number: 42},
		{name: "repo", token: "token", owner: "acme", number: 42},
		{name: "number", token: "token", owner: "acme", repo: "backend"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.GetPullRequestDetail(context.Background(), tt.token, tt.owner, tt.repo, tt.number)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func mustNewClient(t *testing.T, appID string, privateKeyPEM []byte, opts ...ClientOption) *Client {
	t.Helper()
	client, err := NewClient(appID, privateKeyPEM, opts...)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return client
}

func ptrTime(v time.Time) *time.Time {
	return &v
}

func assertAppJWT(t *testing.T, authHeader, appID string, privateKeyPEM []byte, now time.Time) {
	t.Helper()
	if len(authHeader) < len("Bearer ")+1 || authHeader[:7] != "Bearer " {
		t.Fatalf("expected Bearer auth header, got %q", authHeader)
	}
	key := parseTestPrivateKey(t, privateKeyPEM)
	token, err := jwt.Parse(authHeader[7:], func(token *jwt.Token) (any, error) {
		return &key.PublicKey, nil
	}, jwt.WithoutClaimsValidation())
	if err != nil {
		t.Fatalf("parse app jwt: %v", err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("expected MapClaims, got %T", token.Claims)
	}
	if got := claims["iss"]; got != appID {
		t.Fatalf("expected iss %q, got %#v", appID, got)
	}
	if got := int64(claims["iat"].(float64)); got != now.Add(-time.Minute).Unix() {
		t.Fatalf("expected iat %d, got %d", now.Add(-time.Minute).Unix(), got)
	}
	if got := int64(claims["exp"].(float64)); got != now.Add(9*time.Minute).Unix() {
		t.Fatalf("expected exp %d, got %d", now.Add(9*time.Minute).Unix(), got)
	}
}

func testGitHubPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal rsa key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func parseTestPrivateKey(t *testing.T, pemBytes []byte) *rsa.PrivateKey {
	t.Helper()
	key, err := parseGitHubPrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	return key
}
