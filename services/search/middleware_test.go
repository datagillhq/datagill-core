package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestRequestIDHeaderIsSet(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()
	resp := get(t, srv.URL+"/healthz")
	defer resp.Body.Close()
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("expected X-Request-Id response header to be set")
	}
}

func TestJWTMiddleware_MissingToken(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()
	resp := get(t, srv.URL+"/flags")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestJWTMiddleware_ValidToken(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()
	resp := getWithToken(t, srv.URL+"/flags", testOrgID, testUserID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestJWTMiddleware_ExpiredToken(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"organizationId": testOrgID,
		"userId":         testUserID,
		"exp":            time.Now().Add(-time.Minute).Unix(),
	})
	signed, _ := token.SignedString(testRSAKey)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/flags", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestJWTMiddleware_MissingOrgIDClaim(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"userId": testUserID,
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	signed, _ := token.SignedString(testRSAKey)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/flags", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv := newTestServer(t, &stubRepo{})
	defer srv.Close()
	resp := get(t, srv.URL+"/metrics")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
