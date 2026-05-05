package github

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/log"
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
	enrichmentSleepPeriod = 30 * time.Minute
)

// EnrichmentInput is the input for GitHubEnrichmentWorkflow.
type EnrichmentInput struct {
	OrgID uuid.UUID
}

// GitHubEnrichmentWorkflow processes pending PR enrichments for one org.
// It fetches a batch, scans each PR diff for feature flag references in parallel,
// sleeps, then continues as new.
func GitHubEnrichmentWorkflow(ctx workflow.Context, input EnrichmentInput) error {
	logger := workflow.GetLogger(ctx)

	listCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: listPendingTimeout,
	})
	flagsCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: listFlagKeysTimeout,
	})

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

	if err := workflow.Sleep(ctx, enrichmentSleepPeriod); err != nil {
		return err
	}
	return workflow.NewContinueAsNewError(ctx, GitHubEnrichmentWorkflow, input)
}

// processEnrichments fans out enrichment across rows in batches of enrichmentConcurrency.
func processEnrichments(ctx workflow.Context, logger log.Logger, rows []models.PullRequestEnrichment, flagKeys []string) {
	claimCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: claimEnrichmentTimeout,
	})
	enrichCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: enrichPRTimeout,
	})
	saveCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: saveResultTimeout,
	})
	markCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: markFailedTimeout,
	})

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

	if err := workflow.ExecuteActivity(claimCtx, (*ghactivities.Activities).ClaimEnrichment, prKey).Get(ctx, nil); err != nil {
		logger.Warn("skipping enrichment: claim failed", "pr_key", prKey, "error", err)
		return
	}

	var enrichData models.PullRequestEnrichmentData
	if err := workflow.ExecuteActivity(enrichCtx, (*ghactivities.Activities).EnrichPullRequest, ghactivities.EnrichPRInput{
		PullRequestKey: prKey,
		OrgID:          orgID,
		FlagKeys:       flagKeys,
	}).Get(ctx, &enrichData); err != nil {
		logger.Warn("enrichment failed", "pr_key", prKey, "error", err)
		markEnrichmentFailed(ctx, markCtx, logger, prKey)
		return
	}

	if err := workflow.ExecuteActivity(saveCtx, (*ghactivities.Activities).SaveEnrichmentResult, ghactivities.SaveEnrichmentResultInput{
		PullRequestKey: prKey,
		Data:           enrichData,
	}).Get(ctx, nil); err != nil {
		logger.Warn("save enrichment result failed", "pr_key", prKey, "error", err)
		markEnrichmentFailed(ctx, markCtx, logger, prKey)
	}
}

func markEnrichmentFailed(ctx, markCtx workflow.Context, logger log.Logger, prKey uuid.UUID) {
	if err := workflow.ExecuteActivity(markCtx, (*ghactivities.Activities).MarkEnrichmentFailed, prKey).Get(ctx, nil); err != nil {
		logger.Error("mark enrichment failed error", "pr_key", prKey, "error", err)
	}
}
