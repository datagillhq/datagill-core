package main

import (
	"encoding/hex"
	"os"
	"strings"
)

const (
	defaultPort        = "8082"
	defaultEnvironment = "development"
)

type config struct {
	Port                string
	Environment         string
	DatabaseURL         string
	AuthURL             string
	AllowedOrigin       string // CORS allowed origin for the browser UI
	EncryptionKey       []byte // 32 bytes decoded from ENCRYPTION_KEY (hex)
	TemporalAddress     string // host:port of the Temporal frontend
	GitHubAppSlug       string
	GitHubStateKey      string
	GitHubClientID      string
	GitHubClientSecret  string
	GitHubAppID         string
	GitHubPrivateKeyB64 string
	GitHubSpikeMode     bool
}

func loadConfig() config {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	environment := os.Getenv("APP_ENV")
	if environment == "" {
		environment = defaultEnvironment
	}

	var encryptionKey []byte
	if raw := os.Getenv("ENCRYPTION_KEY"); raw != "" {
		decoded, err := hex.DecodeString(raw)
		if err == nil {
			encryptionKey = decoded
		}
	}

	allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:3001"
	}

	return config{
		Port:                port,
		Environment:         environment,
		DatabaseURL:         os.Getenv("APP_DATABASE_URL"),
		AuthURL:             os.Getenv("AUTH_URL"),
		AllowedOrigin:       allowedOrigin,
		EncryptionKey:       encryptionKey,
		TemporalAddress:     os.Getenv("TEMPORAL_ADDRESS"),
		GitHubAppSlug:       os.Getenv("GITHUB_APP_SLUG"),
		GitHubStateKey:      os.Getenv("GITHUB_APP_STATE_SECRET"),
		GitHubClientID:      os.Getenv("GITHUB_APP_CLIENT_ID"),
		GitHubClientSecret:  os.Getenv("GITHUB_APP_CLIENT_SECRET"),
		GitHubAppID:         os.Getenv("GITHUB_APP_ID"),
		GitHubPrivateKeyB64: os.Getenv("GITHUB_APP_PRIVATE_KEY_B64"),
		GitHubSpikeMode:     strings.EqualFold(os.Getenv("GITHUB_AUTH_SPIKE_MODE"), "true"),
	}
}
