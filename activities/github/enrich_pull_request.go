package github

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// EnrichPRInput is the input for the EnrichPullRequest activity.
type EnrichPRInput struct {
	PullRequestKey uuid.UUID
	OrgID          uuid.UUID
	FlagKeys       []string // provider key strings loaded by the workflow before this activity
}

// EnrichPullRequest fetches the PR diff, scans it for flag key references, resolves
// matched keys to DataGill flag UUIDs, and returns the enrichment data.
func (a *Activities) EnrichPullRequest(ctx context.Context, input EnrichPRInput) (models.PullRequestEnrichmentData, error) {
	if len(input.FlagKeys) == 0 {
		return models.PullRequestEnrichmentData{}, nil
	}

	pr, err := a.repo.GetPullRequestByKey(ctx, input.OrgID, input.PullRequestKey)
	if err != nil {
		return models.PullRequestEnrichmentData{}, fmt.Errorf("EnrichPullRequest load pr: %w", err)
	}

	token, err := a.getInstallationToken(ctx, pr.OrgID, pr.IntegrationID)
	if err != nil {
		return models.PullRequestEnrichmentData{}, fmt.Errorf("EnrichPullRequest get token: %w", err)
	}

	rawDiff, err := a.client.FetchRawDiff(
		ctx,
		token.Token,
		pr.Data.Details.Repository.Owner,
		pr.Data.Details.Repository.Name,
		pr.Data.Details.Number,
		pr.Data.Details.Diff.Branches.Target,
		pr.Data.Details.Diff.SHA,
	)
	if err != nil {
		return models.PullRequestEnrichmentData{}, toServiceError(fmt.Errorf("EnrichPullRequest fetch diff: %w", err))
	}

	scanResult, err := ScanDiffForFlags(rawDiff, input.FlagKeys)
	if err != nil {
		return models.PullRequestEnrichmentData{}, fmt.Errorf("EnrichPullRequest scan diff: %w", err)
	}

	addedUUIDs, err := resolveToUUIDs(ctx, a.repo, input.OrgID, scanResult.Added)
	if err != nil {
		return models.PullRequestEnrichmentData{}, fmt.Errorf("EnrichPullRequest resolve added flags: %w", err)
	}

	removedUUIDs, err := resolveToUUIDs(ctx, a.repo, input.OrgID, scanResult.Removed)
	if err != nil {
		return models.PullRequestEnrichmentData{}, fmt.Errorf("EnrichPullRequest resolve removed flags: %w", err)
	}

	return models.PullRequestEnrichmentData{
		FeatureFlags: models.PullRequestFeatureFlagRefs{
			Added:   addedUUIDs,
			Removed: removedUUIDs,
		},
	}, nil
}

func resolveToUUIDs(ctx context.Context, repo githubRepository, orgID uuid.UUID, sourceKeys []string) ([]string, error) {
	if len(sourceKeys) == 0 {
		return nil, nil
	}
	flags, err := repo.GetFeatureFlagsBySourceKeys(ctx, orgID, sourceKeys)
	if err != nil {
		return nil, fmt.Errorf("resolve flag UUIDs: %w", err)
	}
	uuids := make([]string, 0, len(flags))
	for _, f := range flags {
		uuids = append(uuids, f.Key.String())
	}
	return uuids, nil
}
