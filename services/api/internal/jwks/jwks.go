// Package jwks turns an auth server's JSON Web Key Set into a jwt.Keyfunc.
package jwks

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
)

type keySet struct {
	Keys []key `json:"keys"`
}

type key struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// FetchKeyfunc downloads the key set at url and returns a jwt.Keyfunc that accepts
// RS-signed tokens from it. Keys are fetched once; restart the service to pick up
// rotated keys.
func FetchKeyfunc(ctx context.Context, client *http.Client, url string) (jwt.Keyfunc, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build JWKS request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS from %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch JWKS from %s: unexpected status %d", url, resp.StatusCode)
	}

	var set keySet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}
	return keyfuncFor(set)
}

func keyfuncFor(set keySet) (jwt.Keyfunc, error) {
	byKid := make(map[string]*rsa.PublicKey, len(set.Keys))
	// Some providers omit kid in the token header; the first key is used for those.
	var fallback *rsa.PublicKey
	for _, k := range set.Keys {
		// Some providers omit "use"; only keys explicitly for something else are skipped.
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		pub, err := k.rsaPublicKey()
		if err != nil {
			continue
		}
		byKid[k.Kid] = pub
		if fallback == nil {
			fallback = pub
		}
	}
	if fallback == nil {
		return nil, errors.New("JWKS contains no RSA signing keys")
	}

	return func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		if pub, ok := byKid[kid]; ok {
			return pub, nil
		}
		return fallback, nil
	}, nil
}

func (k key) rsaPublicKey() (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(n),
		E: int(new(big.Int).SetBytes(e).Int64()),
	}, nil
}
