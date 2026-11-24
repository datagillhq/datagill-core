package main

import (
	"encoding/base64"
	"encoding/hex"
	"os"
)

const (
	defaultPort        = "8082"
	defaultEnvironment = "development"
)

type config struct {
	Port                  string
	Environment           string
	DatabaseURL           string
	AuthURL               string
	AllowedOrigin         string // CORS allowed origin for the browser UI
	EncryptionKey         []byte // 32 bytes decoded from ENCRYPTION_KEY (hex)
	TemporalAddress       string // host:port of the Temporal frontend
	GitHubAppID           string
	GitHubAppClientID     string
	GitHubAppClientSecret string
	GitHubAppPrivateKey   []byte
	GitHubAppSlug         string
	GitHubAppCallbackURL  string
	GitHubAppStateSecret  string
	FrontendURL           string
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

	var githubPrivateKey []byte
	if raw := os.Getenv("GITHUB_APP_PRIVATE_KEY_B64"); raw != "" {
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err == nil {
			githubPrivateKey = decoded
		}
	}

	allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:3001"
	}

	return config{
		Port:                  port,
		Environment:           environment,
		DatabaseURL:           os.Getenv("APP_DATABASE_URL"),
		AuthURL:               os.Getenv("AUTH_URL"),
		AllowedOrigin:         allowedOrigin,
		EncryptionKey:         encryptionKey,
		TemporalAddress:       os.Getenv("TEMPORAL_ADDRESS"),
		GitHubAppID:           os.Getenv("GITHUB_APP_ID"),
		GitHubAppClientID:     os.Getenv("GITHUB_APP_CLIENT_ID"),
		GitHubAppClientSecret: os.Getenv("GITHUB_APP_CLIENT_SECRET"),
		GitHubAppPrivateKey:   githubPrivateKey,
		GitHubAppSlug:         os.Getenv("GITHUB_APP_SLUG"),
		GitHubAppCallbackURL:  os.Getenv("GITHUB_APP_CALLBACK_URL"),
		GitHubAppStateSecret:  os.Getenv("GITHUB_APP_STATE_SECRET"),
		FrontendURL:           os.Getenv("FRONTEND_URL"),
	}
}
