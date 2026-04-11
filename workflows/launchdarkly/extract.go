package launchdarkly

import (
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"

	ldactivities "github.com/datagill/datagill-core/activities/launchdarkly"
	"github.com/datagill/datagill-core/models"
)

const (
	scrollAuditLogsTimeout = 10 * time.Minute
	flagDetailTimeout      = 30 * time.Second
	updateSyncTimeout      = 10 * time.Second
)

// ExtractInput is the input for LaunchDarklyExtractWorkflow.
type ExtractInput struct {
	Integration models.Integration
}

// flagRef holds the project and flag key parsed from an audit log resource string.
type flagRef struct {
	ProjectKey string
	FlagKey    string
}

// LaunchDarklyExtractWorkflow orchestrates the full ETL extract for one LaunchDarkly integration.
// It scrolls all audit log entries, fetches flag details in parallel, then fans out
// changelog and flag transform child workflows.
func LaunchDarklyExtractWorkflow(ctx workflow.Context, input ExtractInput) error {
	integration := input.Integration

	// Capture the time before scrolling so we record an accurate watermark.
	now := workflow.Now(ctx)

	// --- Step 1: scroll all audit log pages via a single activity call ---
	scrollInput := ldactivities.ScrollAuditLogsInput{
		IntegrationKey: integration.Key,
		OrgID:          integration.OrgID,
	}
	if integration.Config.LastSyncedAt != nil {
		// Incremental run: fetch only entries since the last successful sync.
		scrollInput.After = *integration.Config.LastSyncedAt
	} else if integration.Config.BackfillDays > 0 {
		// First run with backfill configured: fetch entries within the backfill window.
		scrollInput.After = now.AddDate(0, 0, -integration.Config.BackfillDays)
	}
	// If neither is set, no After filter — fetch all available history.

	scrollCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: scrollAuditLogsTimeout,
	})
	var scrollOut ldactivities.ScrollAuditLogsOutput
	// Activity retries are handled by Temporal before .Get() returns; wrapping with %w is safe.
	if err := workflow.ExecuteActivity(scrollCtx, (*ldactivities.Activities).ScrollAuditLogs, scrollInput).Get(ctx, &scrollOut); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: scroll audit logs: %w", err)
	}

	// --- Step 1b: record the sync watermark so subsequent runs are incremental ---
	updateCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: updateSyncTimeout,
	})
	if err := workflow.ExecuteActivity(updateCtx, (*ldactivities.Activities).UpdateLastSyncedAt, ldactivities.UpdateLastSyncedAtInput{
		IntegrationKey: integration.Key,
		SyncedAt:       now,
	}).Get(ctx, nil); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: update last synced at: %w", err)
	}

	// --- Step 2: deduplicate (project, flag) pairs from audit entries ---
	flagRefs := uniqueFlagRefs(scrollOut.Entries)

	// --- Step 3: fetch flag details in parallel ---
	flagDetails, failedKeys := fetchFlagDetails(ctx, integration, flagRefs)

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

	// Fail the workflow if any flag detail fetches exhausted all retries — the operator
	// should retry the workflow to re-attempt those flags.
	if len(failedKeys) > 0 {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: failed to fetch details for %d flag(s): %s",
			len(failedKeys), strings.Join(failedKeys, ", "))
	}
	return nil
}

// uniqueFlagRefs returns deduplicated (project, flag) pairs referenced in the audit entries.
func uniqueFlagRefs(entries []models.LDAuditEntry) []flagRef {
	seen := make(map[string]bool)
	var refs []flagRef
	for _, e := range entries {
		for _, access := range e.Accesses {
			proj := projectKeyFromResource(access.Resource)
			flag := flagKeyFromResource(access.Resource)
			if proj != "" && flag != "" {
				key := proj + "/" + flag
				if !seen[key] {
					seen[key] = true
					refs = append(refs, flagRef{ProjectKey: proj, FlagKey: flag})
				}
			}
		}
	}
	return refs
}

// flagKeyFromResource parses "proj/p:env/e:flag/my-flag" and returns "my-flag".
func flagKeyFromResource(resource string) string {
	for _, part := range splitResource(resource) {
		if before, after, ok := cutPair(part, '/'); ok && before == "flag" {
			return after
		}
	}
	return ""
}

// projectKeyFromResource parses "proj/my-project:env/e:flag/f" and returns "my-project".
func projectKeyFromResource(resource string) string {
	for _, part := range splitResource(resource) {
		if before, after, ok := cutPair(part, '/'); ok && before == "proj" {
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
// Retryable errors that exhaust all Temporal retries are collected and returned as failedKeys.
func fetchFlagDetails(ctx workflow.Context, integration models.Integration, refs []flagRef) ([]ldactivities.GetFlagDetailOutput, []string) {
	type result struct {
		out ldactivities.GetFlagDetailOutput
		err error
	}

	results := make([]result, len(refs))
	wg := workflow.NewWaitGroup(ctx)

	flagCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: flagDetailTimeout,
	})

	for i, ref := range refs {
		i, ref := i, ref
		wg.Add(1)
		workflow.Go(ctx, func(gCtx workflow.Context) {
			defer wg.Done()
			var out ldactivities.GetFlagDetailOutput
			err := workflow.ExecuteActivity(flagCtx, (*ldactivities.Activities).GetFlagDetail, ldactivities.GetFlagDetailInput{
				IntegrationKey: integration.Key,
				OrgID:          integration.OrgID,
				ProjectKey:     ref.ProjectKey,
				FlagKey:        ref.FlagKey,
			}).Get(gCtx, &out)
			results[i] = result{out: out, err: err}
		})
	}
	wg.Wait(ctx)

	var details []ldactivities.GetFlagDetailOutput
	var failedKeys []string
	for i, r := range results {
		if r.err != nil {
			if isNonRetryable(r.err) {
				// Flag was deleted or otherwise permanently unavailable — skip.
				continue
			}
			// All Temporal retries exhausted for this flag — caller will fail the workflow.
			failedKeys = append(failedKeys, refs[i].ProjectKey+"/"+refs[i].FlagKey)
			continue
		}
		details = append(details, r.out)
	}
	return details, failedKeys
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
		ProjectKey:      detail.ProjectKey,
	}
	future := workflow.ExecuteChildWorkflow(childCtx, LaunchDarklyFlagTransformWorkflow, input)
	if err := future.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: start flag transform for %s: %w", detail.Flag.Key, err)
	}
	return nil
}
