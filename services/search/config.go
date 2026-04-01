package main

import (
	"encoding/hex"
	"os"
)

const (
	defaultPort        = "8082"
	defaultEnvironment = "development"
)

type config struct {
	Port          string
	Environment   string
	DatabaseURL   string
	AuthURL       string
	EncryptionKey []byte // 32 bytes decoded from ENCRYPTION_KEY (hex)
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

	return config{
		Port:          port,
		Environment:   environment,
		DatabaseURL:   os.Getenv("APP_DATABASE_URL"),
		AuthURL:       os.Getenv("AUTH_URL"),
		EncryptionKey: encryptionKey,
	}
}
