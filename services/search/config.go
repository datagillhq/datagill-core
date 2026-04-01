package main

import "os"

const (
	defaultPort        = "8082"
	defaultEnvironment = "development"
)

type config struct {
	Port          string
	Environment   string
	DatabaseURL   string
	BetterAuthURL string
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

	return config{
		Port:         port,
		Environment:  environment,
		DatabaseURL:  os.Getenv("APP_DATABASE_URL"),
		BetterAuthURL: os.Getenv("BETTERAUTH_URL"),
	}
}
