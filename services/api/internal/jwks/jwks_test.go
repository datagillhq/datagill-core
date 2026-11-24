package jwks

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestFetchKeyfunc_VerifiesTokensSignedWithPublishedKey(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(keySet{Keys: []key{
			{Kty: "EC", Kid: "ignored"},
			{
				Kty: "RSA",
				Use: "sig",
				Kid: "k1",
				N:   base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
				E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
			},
		}})
	}))
	defer srv.Close()

	keyfunc, err := FetchKeyfunc(t.Context(), &http.Client{Timeout: 5 * time.Second}, srv.URL)
	if err != nil {
		t.Fatalf("FetchKeyfunc: %v", err)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()})
	token.Header["kid"] = "k1"
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jwt.Parse(signed, keyfunc); err != nil {
		t.Fatalf("expected token to verify, got %v", err)
	}

	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{})
	hsSigned, _ := hs.SignedString([]byte("secret"))
	if _, err := jwt.Parse(hsSigned, keyfunc); err == nil {
		t.Fatal("expected HMAC token to be rejected")
	}
}

func TestFetchKeyfunc_Errors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"non-200", http.StatusInternalServerError, `{}`},
		{"bad json", http.StatusOK, `{`},
		{"no rsa keys", http.StatusOK, `{"keys":[{"kty":"EC"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			if _, err := FetchKeyfunc(t.Context(), &http.Client{Timeout: 5 * time.Second}, srv.URL); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
