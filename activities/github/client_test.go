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
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

func mustNewClient(t *testing.T, appID string, privateKeyPEM []byte, opts ...ClientOption) *Client {
	t.Helper()
	client, err := NewClient(appID, privateKeyPEM, opts...)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return client
}

func assertAppJWT(t *testing.T, authHeader, appID string, privateKeyPEM []byte, now time.Time) {
	t.Helper()
	if len(authHeader) < len("Bearer ")+1 || authHeader[:7] != "Bearer " {
		t.Fatalf("expected Bearer auth header, got %q", authHeader)
	}
	key := parseTestPrivateKey(t, privateKeyPEM)
	token, err := jwt.Parse(authHeader[7:], func(token *jwt.Token) (any, error) {
		return &key.PublicKey, nil
	})
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
