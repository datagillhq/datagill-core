// DO NOT USE IN PRODUCTION.
// auth-dev is a local development helper that issues RS256 JWTs and exposes a
// JWKS endpoint so the search service can validate them. It generates a fresh
// RSA key pair on every startup — tokens are invalid after a restart.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	port  = ":3001"
	keyID = "dev-key-1"
)

var privateKey *rsa.PrivateKey

func main() {
	var err error
	privateKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("generate rsa key: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/jwks.json", jwksHandler)
	mux.HandleFunc("POST /token", tokenHandler)

	log.Printf("auth-dev listening on %s", port)
	log.Printf("  JWKS: http://localhost%s/.well-known/jwks.json", port)
	log.Printf("  Token: POST http://localhost%s/token  body: {\"org_id\":\"<uuid>\",\"user_id\":\"<uuid>\"}", port)
	log.Fatal(http.ListenAndServe(port, mux))
}

// jwksHandler serves the RSA public key in JWK Set format.
func jwksHandler(w http.ResponseWriter, _ *http.Request) {
	pub := &privateKey.PublicKey
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": keyID,
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

type tokenRequest struct {
	OrgID  string `json:"org_id"`
	UserID string `json:"user_id"`
}

// tokenHandler issues a signed RS256 JWT with organizationId and userId claims.
func tokenHandler(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrgID == "" || req.UserID == "" {
		http.Error(w, `{"error":"org_id and user_id required"}`, http.StatusBadRequest)
		return
	}

	t := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"organizationId": req.OrgID,
		"userId":         req.UserID,
		"exp":            time.Now().Add(24 * time.Hour).Unix(),
	})
	t.Header["kid"] = keyID

	signed, err := t.SignedString(privateKey)
	if err != nil {
		http.Error(w, `{"error":"sign failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": signed})
}
