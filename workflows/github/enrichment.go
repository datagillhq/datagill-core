package github

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	ghactivities "github.com/datagill/datagill-core/activities/github"
	"github.com/datagill/datagill-core/models"
)

const (
	listPendingTimeout     = 30 * time.Second
	claimEnrichmentTimeout = 10 * time.Second
	listFlagKeysTimeout    = 30 * time.Second
	enrichPRTimeout        = 5 * time.Minute
	saveResultTimeout      = 30 * time.Second
	markFailedTimeout      = 10 * time.Second

	enrichmentBatchSize   = 100
	enrichmentConcurrency = 10

	// enrichmentMaxActivityAttempts bounds Temporal's retries within one run. Temporal's
	// default is unlimited, which would leave a persistently failing PR in_progress forever
	// and block the batch. Once retries are exhausted the PR is marked failed and the
	// attempts cap in pull_request_enrichments decides whether a later run tries again.
	enrichmentMaxActivityAttempts = 3

	// enrichmentRetryWaitBudget is the time allowed for waits between an activity's
	// attempts, such as a GitHub rate limit's Retry-After. A longer wait ends the activity
	// and the PR falls back to the cross-run backoff instead of holding the batch.
	enrichmentRetryWaitBudget = 5 * time.Minute
)

// EnrichmentInput is the input for GitHubEnrichmentWorkflow.
type EnrichmentInput struct {
	OrgID uuid.UUID
}

// GitHubEnrichmentWorkflow processes one batch of pending PR enrichments for one org,
// scanning each PR diff for feature flag references in parallel. It runs once per
// invocation; the per-org Temporal schedule owns the cadence.
func GitHubEnrichmentWorkflow(ctx workflow.Context, input EnrichmentInput) error {
	logger := workflow.GetLogger(ctx)

	listCtx := withEnrichmentActivityOptions(ctx, listPendingTimeout)
	flagsCtx := withEnrichmentActivityOptions(ctx, listFlagKeysTimeout)

	var rows []models.PullRequestEnrichment
	if err := workflow.ExecuteActivity(listCtx, (*ghactivities.Activities).ListPendingEnrichments, ghactivities.ListPendingEnrichmentsInput{
		OrgID: input.OrgID,
		Limit: enrichmentBatchSize,
	}).Get(ctx, &rows); err != nil {
		return fmt.Errorf("GitHubEnrichmentWorkflow: list pending: %w", err)
	}

	// Fetch flag keys once for the org — all PRs in the batch share the same org.
	var flagKeys []string
	if len(rows) > 0 {
		if err := workflow.ExecuteActivity(flagsCtx, (*ghactivities.Activities).ListFlagSourceKeys, ghactivities.ListFlagSourceKeysInput{
			OrgID: input.OrgID,
		}).Get(ctx, &flagKeys); err != nil {
			return fmt.Errorf("GitHubEnrichmentWorkflow: list flag keys: %w", err)
		}
	}

	processEnrichments(ctx, logger, rows, flagKeys)
	return nil
}

// withEnrichmentActivityOptions applies the timeout and bounded retry policy shared by
// every activity in GitHubEnrichmentWorkflow.
//
// ScheduleToCloseTimeout caps the total time across attempts, including retry waits.
// Without it a long Retry-After keeps a PR in_progress past the repository's 1-hour stale
// window, so another run could reclaim it while this one is still working. Worst case per
// PR (claim + enrich + save + mark failed) is under 40 minutes.
func withEnrichmentActivityOptions(ctx workflow.Context, timeout time.Duration) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    timeout,
		ScheduleToCloseTimeout: enrichmentMaxActivityAttempts*timeout + enrichmentRetryWaitBudget,
		RetryPolicy:            &temporal.RetryPolicy{MaximumAttempts: enrichmentMaxActivityAttempts},
	})
}

// processEnrichments fans out enrichment across rows in batches of enrichmentConcurrency.
func processEnrichments(ctx workflow.Context, logger log.Logger, rows []models.PullRequestEnrichment, flagKeys []string) {
	claimCtx := withEnrichmentActivityOptions(ctx, claimEnrichmentTimeout)
	enrichCtx := withEnrichmentActivityOptions(ctx, enrichPRTimeout)
	saveCtx := withEnrichmentActivityOptions(ctx, saveResultTimeout)
	markCtx := withEnrichmentActivityOptions(ctx, markFailedTimeout)

	for start := 0; start < len(rows); start += enrichmentConcurrency {
		end := start + enrichmentConcurrency
		if end > len(rows) {
			end = len(rows)
		}

		wg := workflow.NewWaitGroup(ctx)
		for _, row := range rows[start:end] {
			row := row
			wg.Add(1)
			workflow.Go(ctx, func(gCtx workflow.Context) {
				defer wg.Done()
				enrichOne(gCtx, logger, claimCtx, enrichCtx, saveCtx, markCtx, row, flagKeys)
			})
		}
		wg.Wait(ctx)
	}
}

func enrichOne(
	ctx workflow.Context,
	logger log.Logger,
	claimCtx, enrichCtx, saveCtx, markCtx workflow.Context,
	row models.PullRequestEnrichment,
	flagKeys []string,
) {
	prKey := row.PullRequestKey
	orgID := row.OrgID
	rowInput := ghactivities.EnrichmentRowInput{OrgID: orgID, PullRequestKey: prKey}

	var claimed bool
	if err := workflow.ExecuteActivity(claimCtx, (*ghactivities.Activities).ClaimEnrichment, rowInput).Get(ctx, &claimed); err != nil {
		logger.Error("skipping enrichment: claim failed", "pr_key", prKey, "error", err)
		return
	}
	if !claimed {
		logger.Info("skipping enrichment: already claimed or finished", "pr_key", prKey)
		return
	}

	var enrichData models.PullRequestEnrichmentData
	if err := workflow.ExecuteActivity(enrichCtx, (*ghactivities.Activities).EnrichPullRequest, ghactivities.EnrichPRInput{
		PullRequestKey: prKey,
		OrgID:          orgID,
		FlagKeys:       flagKeys,
	}).Get(ctx, &enrichData); err != nil {
		logger.Error("enrichment failed after retries", "pr_key", prKey, "error", err)
		markEnrichmentFailed(ctx, markCtx, logger, rowInput)
		return
	}

	if err := workflow.ExecuteActivity(saveCtx, (*ghactivities.Activities).SaveEnrichmentResult, ghactivities.SaveEnrichmentResultInput{
		OrgID:          orgID,
		PullRequestKey: prKey,
		Data:           enrichData,
	}).Get(ctx, nil); err != nil {
		logger.Error("save enrichment result failed after retries", "pr_key", prKey, "error", err)
		markEnrichmentFailed(ctx, markCtx, logger, rowInput)
	}
}

func markEnrichmentFailed(ctx, markCtx workflow.Context, logger log.Logger, row ghactivities.EnrichmentRowInput) {
	if err := workflow.ExecuteActivity(markCtx, (*ghactivities.Activities).MarkEnrichmentFailed, row).Get(ctx, nil); err != nil {
		logger.Error("mark enrichment failed error", "pr_key", row.PullRequestKey, "error", err)
	}
}
