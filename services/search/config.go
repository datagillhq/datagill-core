package main

import "os"

const (
	defaultPort        = "8082"
	defaultEnvironment = "development"
)

type config struct {
	Port        string
	Environment string
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
		Port:        port,
		Environment: environment,
	}
}
