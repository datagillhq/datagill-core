package github

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

type githubRepository interface {
	GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error)
}

// Activities contains Temporal activities for GitHub ETL.
type Activities struct {
	client *Client
	repo   githubRepository
}

// New creates a new Activities instance.
func New(client *Client, repo githubRepository) *Activities {
	return &Activities{client: client, repo: repo}
}

func (a *Activities) installationTokenFor(ctx context.Context, orgID, key uuid.UUID) (InstallationToken, error) {
	if a.client == nil {
		return InstallationToken{}, fmt.Errorf("github client is not configured")
	}

	integration, err := a.repo.GetIntegration(ctx, orgID, key)
	if err != nil {
		return InstallationToken{}, fmt.Errorf("get integration for client: %w", err)
	}
	if integration.Source != models.SourceGitHub {
		return InstallationToken{}, fmt.Errorf("integration source %q is not github", integration.Source)
	}
	if integration.Config.InstallationID == 0 {
		return InstallationToken{}, fmt.Errorf("github integration missing installation_id")
	}

	token, err := a.client.CreateInstallationToken(ctx, integration.Config.InstallationID)
	if err != nil {
		return InstallationToken{}, toServiceError(fmt.Errorf("create installation token: %w", err))
	}
	return token, nil
}
