package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"log"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/datagill/datagill-core/activities"
	ghactivities "github.com/datagill/datagill-core/activities/github"
	ldactivities "github.com/datagill/datagill-core/activities/launchdarkly"
	"github.com/datagill/datagill-core/repository"
	"github.com/datagill/datagill-core/workflows"
	ghworkflows "github.com/datagill/datagill-core/workflows/github"
	ldworkflows "github.com/datagill/datagill-core/workflows/launchdarkly"
)

const taskQueue = "etl"

func main() {
	dbURL := mustEnv("APP_DATABASE_URL")
	temporalAddr := mustEnv("TEMPORAL_ADDRESS")
	encKeyHex := mustEnv("ENCRYPTION_KEY")

	encKey, err := hex.DecodeString(encKeyHex)
	if err != nil || len(encKey) != 32 {
		log.Fatalf("ENCRYPTION_KEY must be a 64-char hex string (32 bytes): %v", err)
	}

	repo, err := repository.NewPostgresRepository(context.Background(), dbURL, encKey)
	if err != nil {
		log.Fatalf("connect to postgres: %v", err)
	}

	tc, err := client.Dial(client.Options{HostPort: temporalAddr})
	if err != nil {
		log.Fatalf("dial temporal: %v", err)
	}
	defer tc.Close()

	w := worker.New(tc, taskQueue, worker.Options{})

	// Shared activities (SaveSourceRecord, SaveFeatureFlag, SaveChangelog, GetIntegration).
	w.RegisterActivity(activities.NewSharedActivities(repo))

	// LaunchDarkly activities. client=nil means each activity fetches the integration's
	// API key from the repo at runtime — supports multiple integrations on one worker.
	w.RegisterActivity(ldactivities.New(nil, repo))

	w.RegisterActivity(ghactivities.New(githubClientFromEnv(), repo))

	w.RegisterWorkflow(workflows.FeatureFlagETLWorkflow)
	w.RegisterWorkflow(ghworkflows.GitHubExtractWorkflow)
	w.RegisterWorkflow(ghworkflows.GitHubEnrichmentWorkflow)
	w.RegisterWorkflow(ldworkflows.LaunchDarklyExtractWorkflow)

	log.Printf("worker starting on task queue %q → temporal %s", taskQueue, temporalAddr)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker error: %v", err)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func githubClientFromEnv() *ghactivities.Client {
	appID := os.Getenv("GITHUB_APP_ID")
	privateKeyB64 := os.Getenv("GITHUB_APP_PRIVATE_KEY_B64")
	if appID == "" && privateKeyB64 == "" {
		log.Print("GitHub App env vars are not configured; GitHub activities will fail until configured")
		return nil
	}
	if appID == "" || privateKeyB64 == "" {
		log.Fatal("GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY_B64 must both be set for GitHub ETL")
	}
	privateKeyPEM, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		log.Fatalf("decode GITHUB_APP_PRIVATE_KEY_B64: %v", err)
	}
	client, err := ghactivities.NewClient(appID, privateKeyPEM)
	if err != nil {
		log.Fatalf("create GitHub client: %v", err)
	}
	return client
}
