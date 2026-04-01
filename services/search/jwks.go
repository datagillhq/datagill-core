package main

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// fetchJWKSKeyfunc fetches the JWKS from the given URL, parses all RSA signing
// keys, and returns a jwt.Keyfunc that validates tokens against them. The keys
// are cached for the lifetime of the process — restart to pick up rotated keys.
func fetchJWKSKeyfunc(jwksURL string) (jwt.Keyfunc, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(jwksURL) //nolint:noctx // startup-only fetch, no request context available
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS from %s: %w", jwksURL, err)
	}
	defer resp.Body.Close()

	var body jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding JWKS: %w", err)
	}

	// Index keys by kid for fast lookup; keep the first key as a fallback for
	// providers that omit kid in the token header.
	keyMap := make(map[string]*rsa.PublicKey, len(body.Keys))
	var fallback *rsa.PublicKey
	for _, k := range body.Keys {
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}
		pub, err := parseRSAPublicKey(k)
		if err != nil {
			continue
		}
		keyMap[k.Kid] = pub
		if fallback == nil {
			fallback = pub
		}
	}
	if fallback == nil {
		return nil, fmt.Errorf("no RSA signing keys found in JWKS response from %s", jwksURL)
	}

	return func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		if key, ok := keyMap[kid]; ok {
			return key, nil
		}
		return fallback, nil
	}, nil
}

func parseRSAPublicKey(k jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decoding n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decoding e: %w", err)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}, nil
}
