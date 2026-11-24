package github

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// ListPendingEnrichmentsInput is the input for ListPendingEnrichments.
type ListPendingEnrichmentsInput struct {
	OrgID uuid.UUID
	Limit int
}

// ListPendingEnrichments returns pending enrichment rows for an org up to the given limit.
func (a *Activities) ListPendingEnrichments(ctx context.Context, input ListPendingEnrichmentsInput) ([]models.PullRequestEnrichment, error) {
	rows, err := a.repo.ListPendingEnrichments(ctx, input.OrgID, input.Limit)
	if err != nil {
		return nil, fmt.Errorf("ListPendingEnrichments: %w", err)
	}
	return rows, nil
}

// EnrichmentRowInput identifies one pull_request_enrichments row. OrgID scopes every query
// to the tenant that owns the row.
type EnrichmentRowInput struct {
	OrgID          uuid.UUID
	PullRequestKey uuid.UUID
}

// ClaimEnrichment transitions a claimable enrichment to in_progress.
// It returns false when another run already holds or finished the row.
func (a *Activities) ClaimEnrichment(ctx context.Context, input EnrichmentRowInput) (bool, error) {
	claimed, err := a.repo.ClaimEnrichment(ctx, input.OrgID, input.PullRequestKey)
	if err != nil {
		return false, fmt.Errorf("ClaimEnrichment: %w", err)
	}
	return claimed, nil
}

// SaveEnrichmentResultInput is the input for SaveEnrichmentResult.
type SaveEnrichmentResultInput struct {
	OrgID          uuid.UUID
	PullRequestKey uuid.UUID
	Data           models.PullRequestEnrichmentData
}

// SaveEnrichmentResult writes enrichment output and marks the row completed.
func (a *Activities) SaveEnrichmentResult(ctx context.Context, input SaveEnrichmentResultInput) error {
	if err := a.repo.SaveEnrichmentResult(ctx, input.OrgID, input.PullRequestKey, input.Data); err != nil {
		return fmt.Errorf("SaveEnrichmentResult: %w", err)
	}
	return nil
}

// MarkEnrichmentFailed resets to pending for retry or marks failed when the attempt cap is reached.
func (a *Activities) MarkEnrichmentFailed(ctx context.Context, input EnrichmentRowInput) error {
	if err := a.repo.MarkEnrichmentFailed(ctx, input.OrgID, input.PullRequestKey); err != nil {
		return fmt.Errorf("MarkEnrichmentFailed: %w", err)
	}
	return nil
}

// ListFlagSourceKeysInput is the input for ListFlagSourceKeys.
type ListFlagSourceKeysInput struct {
	OrgID uuid.UUID
}

// ListFlagSourceKeys returns all non-deleted flag provider key strings for an org.
func (a *Activities) ListFlagSourceKeys(ctx context.Context, input ListFlagSourceKeysInput) ([]string, error) {
	keys, err := a.repo.ListFeatureFlagSourceKeys(ctx, input.OrgID)
	if err != nil {
		return nil, fmt.Errorf("ListFlagSourceKeys: %w", err)
	}
	return keys, nil
}
