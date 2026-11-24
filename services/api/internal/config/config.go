// Package config loads and validates the API service configuration from the environment.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// Config is the full API service configuration.
type Config struct {
	Port          string
	Environment   string
	DatabaseURL   string
	AuthURL       string
	AllowedOrigin string // CORS origin of the browser UI
	FrontendURL   string // base URL the GitHub install callback redirects back to
	EncryptionKey []byte // 32 bytes, used to encrypt provider API keys at rest
	Temporal      string // host:port of the Temporal frontend
	GitHubApp     GitHubApp
}

// GitHubApp holds the GitHub App credentials. All fields are optional: without them the
// GitHub install flow returns an error and every other endpoint works as usual.
type GitHubApp struct {
	AppID        string
	ClientID     string
	ClientSecret string
	PrivateKey   []byte // PEM, decoded from GITHUB_APP_PRIVATE_KEY_B64
	Slug         string
	CallbackURL  string
	StateSecret  string
}

// Load reads the configuration from environment variables and reports every missing or
// malformed value at once, so a misconfigured deploy fails with one clear message.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	var errs []error

	cfg := Config{
		Port:          withDefault(getenv("PORT"), "8082"),
		Environment:   withDefault(getenv("APP_ENV"), "development"),
		DatabaseURL:   getenv("APP_DATABASE_URL"),
		AuthURL:       getenv("AUTH_URL"),
		AllowedOrigin: withDefault(getenv("ALLOWED_ORIGIN"), "http://localhost:3001"),
		FrontendURL:   getenv("FRONTEND_URL"),
		Temporal:      getenv("TEMPORAL_ADDRESS"),
		GitHubApp: GitHubApp{
			AppID:        getenv("GITHUB_APP_ID"),
			ClientID:     getenv("GITHUB_APP_CLIENT_ID"),
			ClientSecret: getenv("GITHUB_APP_CLIENT_SECRET"),
			Slug:         getenv("GITHUB_APP_SLUG"),
			CallbackURL:  getenv("GITHUB_APP_CALLBACK_URL"),
			StateSecret:  getenv("GITHUB_APP_STATE_SECRET"),
		},
	}

	for name, value := range map[string]string{
		"APP_DATABASE_URL": cfg.DatabaseURL,
		"AUTH_URL":         cfg.AuthURL,
		"TEMPORAL_ADDRESS": cfg.Temporal,
	} {
		if value == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}

	key, err := hex.DecodeString(getenv("ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		errs = append(errs, errors.New("ENCRYPTION_KEY must be a 64-character hex string (32 bytes); generate with: openssl rand -hex 32"))
	}
	cfg.EncryptionKey = key

	if raw := getenv("GITHUB_APP_PRIVATE_KEY_B64"); raw != "" {
		pem, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("GITHUB_APP_PRIVATE_KEY_B64 is not valid base64: %w", err))
		}
		cfg.GitHubApp.PrivateKey = pem
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func withDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
