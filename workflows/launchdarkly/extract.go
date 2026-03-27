package launchdarkly

import (
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"

	ldactivities "github.com/datagill/datagill-core/activities/launchdarkly"
	"github.com/datagill/datagill-core/models"
)

const (
	scrollAuditLogsTimeout = 10 * time.Minute
	flagDetailTimeout      = 30 * time.Second
)

// ExtractInput is the input for LaunchDarklyExtractWorkflow.
type ExtractInput struct {
	Integration models.Integration
}

// LaunchDarklyExtractWorkflow orchestrates the full ETL extract for one LaunchDarkly integration.
// It scrolls all audit log entries, fetches flag details in parallel, then fans out
// changelog and flag transform child workflows.
func LaunchDarklyExtractWorkflow(ctx workflow.Context, input ExtractInput) error {
	integration := input.Integration

	// --- Step 1: scroll all audit log pages via a single activity call ---
	scrollInput := ldactivities.ScrollAuditLogsInput{
		IntegrationKey: integration.Key,
		OrgID:          integration.OrgID,
		ProjectKey:     integration.Config.ProjectKey,
	}
	if integration.Config.BackfillDays > 0 {
		scrollInput.After = workflow.Now(ctx).AddDate(0, 0, -integration.Config.BackfillDays)
	}

	scrollCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: scrollAuditLogsTimeout,
	})
	var scrollOut ldactivities.ScrollAuditLogsOutput
	if err := workflow.ExecuteActivity(scrollCtx, (*ldactivities.Activities).ScrollAuditLogs, scrollInput).Get(ctx, &scrollOut); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: scroll audit logs: %w", err)
	}

	// --- Step 2: deduplicate flag keys ---
	flagKeys := uniqueFlagKeys(scrollOut.Entries)

	// --- Step 3: fetch flag details in parallel ---
	flagDetails := fetchFlagDetails(ctx, integration, flagKeys)

	// --- Step 4: fan-out changelog transforms (fire-and-forget + ack) ---
	for _, entry := range scrollOut.Entries {
		if entry.Kind != "flag" {
			continue
		}
		if err := spawnChangelogTransform(ctx, integration, entry); err != nil {
			return err
		}
	}

	// --- Step 5: fan-out flag transforms (fire-and-forget + ack) ---
	for _, detail := range flagDetails {
		if err := spawnFlagTransform(ctx, integration, detail); err != nil {
			return err
		}
	}

	return nil
}

// uniqueFlagKeys returns the deduplicated flag keys referenced in the audit entries.
func uniqueFlagKeys(entries []models.LDAuditEntry) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, e := range entries {
		for _, access := range e.Accesses {
			if key := flagKeyFromResource(access.Resource); key != "" && !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// flagKeyFromResource parses "proj/p:env/e:flag/my-flag" and returns "my-flag".
func flagKeyFromResource(resource string) string {
	// reuse the same segment-parsing logic as normalize_changelog
	for _, part := range splitResource(resource) {
		if before, after, ok := cutPair(part, '/'); ok && before == "flag" {
			return after
		}
	}
	return ""
}

func splitResource(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func cutPair(s string, sep byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// fetchFlagDetails fetches all flag details in parallel using workflow.Go goroutines.
// Non-retryable errors (e.g. flag deleted) are skipped silently.
func fetchFlagDetails(ctx workflow.Context, integration models.Integration, flagKeys []string) []ldactivities.GetFlagDetailOutput {
	type result struct {
		out ldactivities.GetFlagDetailOutput
		err error
	}

	results := make([]result, len(flagKeys))
	wg := workflow.NewWaitGroup(ctx)

	flagCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: flagDetailTimeout,
	})

	for i, key := range flagKeys {
		i, key := i, key
		wg.Add(1)
		workflow.Go(ctx, func(gCtx workflow.Context) {
			defer wg.Done()
			var out ldactivities.GetFlagDetailOutput
			err := workflow.ExecuteActivity(flagCtx, (*ldactivities.Activities).GetFlagDetail, ldactivities.GetFlagDetailInput{
				IntegrationKey: integration.Key,
				OrgID:          integration.OrgID,
				ProjectKey:     integration.Config.ProjectKey,
				FlagKey:        key,
			}).Get(gCtx, &out)
			results[i] = result{out: out, err: err}
		})
	}
	wg.Wait(ctx)

	var details []ldactivities.GetFlagDetailOutput
	for _, r := range results {
		if r.err != nil {
			if isNonRetryable(r.err) {
				// Flag was deleted or otherwise permanently unavailable — skip.
				continue
			}
			// Retryable errors would have been retried by Temporal before reaching here.
			// If we reach this point it means retries were exhausted; skip to avoid blocking.
			continue
		}
		details = append(details, r.out)
	}
	return details
}

// spawnChangelogTransform fires off a ChangelogTransformWorkflow and waits for it to start (ack).
func spawnChangelogTransform(ctx workflow.Context, integration models.Integration, entry models.LDAuditEntry) error {
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        fmt.Sprintf("ld-changelog-%s-%s", integration.Key, entry.ID),
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	})
	input := ChangelogTransformInput{
		Entry:          entry,
		IntegrationKey: integration.Key,
		OrgID:          integration.OrgID,
		ProjectKey:     integration.Config.ProjectKey,
	}
	future := workflow.ExecuteChildWorkflow(childCtx, LaunchDarklyChangelogTransformWorkflow, input)
	if err := future.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: start changelog transform for %s: %w", entry.ID, err)
	}
	return nil
}

// spawnFlagTransform fires off a FlagTransformWorkflow and waits for it to start (ack).
func spawnFlagTransform(ctx workflow.Context, integration models.Integration, detail ldactivities.GetFlagDetailOutput) error {
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        fmt.Sprintf("ld-flag-%s-%s", integration.Key, detail.Flag.Key),
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	})
	input := FlagTransformInput{
		Flag:            detail.Flag,
		SourceRecordKey: detail.SourceRecordKey,
		IntegrationKey:  integration.Key,
		OrgID:           integration.OrgID,
		ProjectKey:      integration.Config.ProjectKey,
	}
	future := workflow.ExecuteChildWorkflow(childCtx, LaunchDarklyFlagTransformWorkflow, input)
	if err := future.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: start flag transform for %s: %w", detail.Flag.Key, err)
	}
	return nil
}
