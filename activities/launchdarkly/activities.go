// Package launchdarkly provides Temporal activities for the LaunchDarkly ETL pipeline.
package launchdarkly

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// Activities contains Temporal activities for LaunchDarkly ETL.
type Activities struct {
	// client is optional. When non-nil it is used directly (e.g. in tests).
	// When nil the activity fetches the integration from the repo and constructs
	// the client from the stored API key — enabling multi-tenant workers.
	client *Client
	repo   launchDarklyRepository
}

type launchDarklyRepository interface {
	GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
	SaveSourceRecord(ctx context.Context, r models.SourceRecord) error
	UpdateIntegrationLastSyncedAt(ctx context.Context, orgID, key uuid.UUID, syncedAt time.Time) error
}

// New creates a new Activities instance.
// Pass client=nil in production workers; clientFor will build one per integration at runtime.
func New(client *Client, repo launchDarklyRepository) *Activities {
	return &Activities{client: client, repo: repo}
}

// clientFor returns the LD client to use for the given integration.
// If a.client is non-nil (set at construction time, typically in tests) it is returned
// directly. Otherwise the integration is fetched from the repository and a client is
// constructed from its decrypted API key.
func (a *Activities) clientFor(ctx context.Context, orgID, key uuid.UUID) (*Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	integration, err := a.repo.GetIntegration(ctx, orgID, key)
	if err != nil {
		return nil, fmt.Errorf("get integration for client: %w", err)
	}
	c, err := NewClient(integration.APIKey)
	if err != nil {
		return nil, fmt.Errorf("build LD client: %w", err)
	}
	return c, nil
}
