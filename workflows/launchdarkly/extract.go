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
	updateLastSyncedTimeout = 10 * time.Second
)

// ExtractInput is the input for LaunchDarklyExtractWorkflow.
type ExtractInput struct {
	Integration models.Integration
}

// LaunchDarklyExtractWorkflow orchestrates the full ETL extract for one LaunchDarkly integration.
// It scrolls all audit log entries since the last sync (or all-time on first run), fetches flag
// details in parallel, fans out changelog and flag transform child workflows, then records the
// high-water mark for the next incremental sync.
func LaunchDarklyExtractWorkflow(ctx workflow.Context, input ExtractInput) error {
	integration := input.Integration
	logger := workflow.GetLogger(ctx)

	// Capture current time before any I/O so the watermark is conservative.
	// workflow.Now always returns UTC — Temporal's deterministic clock is timezone-agnostic.
	// The value is stored in Postgres as TIMESTAMPTZ, which is always UTC on the wire via pgx.
	syncedAt := workflow.Now(ctx)

	// --- Step 1: scroll all audit log pages ---
	scrollInput := ldactivities.ScrollAuditLogsInput{
		IntegrationKey: integration.Key,
		OrgID:          integration.OrgID,
	}
	if integration.LastSyncedAt != nil {
		// Incremental sync: only fetch entries after the last watermark.
		scrollInput.After = *integration.LastSyncedAt
	}

	scrollCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: scrollAuditLogsTimeout,
	})
	var scrollOut ldactivities.ScrollAuditLogsOutput
	if err := workflow.ExecuteActivity(scrollCtx, (*ldactivities.Activities).ScrollAuditLogs, scrollInput).Get(ctx, &scrollOut); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: scroll audit logs: %w", err)
	}

	// --- Step 2: deduplicate (project, flag) pairs from resources ---
	refs := uniqueFlagRefs(scrollOut.Entries)

	// --- Step 3: fetch flag details in parallel ---
	details := fetchFlagDetails(ctx, integration, refs)

	// --- Step 4: fan-out changelog transforms ---
	for _, entry := range scrollOut.Entries {
		if entry.Kind != "flag" {
			continue
		}
		projKey := projectKeyFromEntry(entry)
		if projKey == "" {
			// LD resource string did not contain a "proj/<key>" segment. This is unexpected
			// for flag entries and likely means an unrecognised resource format. Log and skip
			// rather than failing the whole sync — other entries may be valid.
			logger.Warn("skipping flag entry: could not extract project key from resources",
				"entryID", entry.ID,
				"accesses", entry.Accesses,
			)
			continue
		}
		if err := spawnChangelogTransform(ctx, integration, entry, projKey); err != nil {
			return err
		}
	}

	// --- Step 5: fan-out flag transforms ---
	for _, d := range details {
		if err := spawnFlagTransform(ctx, integration, d.ref.ProjectKey, d.detail); err != nil {
			return err
		}
	}

	// --- Step 6: record high-water mark ---
	syncCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: updateLastSyncedTimeout,
	})
	if err := workflow.ExecuteActivity(syncCtx, (*ldactivities.Activities).UpdateLastSyncedAt, ldactivities.UpdateLastSyncedAtInput{
		IntegrationKey: integration.Key,
		SyncedAt:       syncedAt,
	}).Get(ctx, nil); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: update last synced at: %w", err)
	}

	return nil
}

// flagRef is a unique (project, flag) pair derived from audit log resources.
type flagRef struct {
	ProjectKey string
	FlagKey    string
}

// uniqueFlagRefs returns deduplicated (project, flag) pairs referenced in the audit entries.
func uniqueFlagRefs(entries []models.LDAuditEntry) []flagRef {
	seen := make(map[flagRef]bool)
	var refs []flagRef
	for _, e := range entries {
		for _, access := range e.Accesses {
			proj := projectKeyFromResource(access.Resource)
			flag := flagKeyFromResource(access.Resource)
			if proj != "" && flag != "" {
				ref := flagRef{ProjectKey: proj, FlagKey: flag}
				if !seen[ref] {
					seen[ref] = true
					refs = append(refs, ref)
				}
			}
		}
	}
	return refs
}

// projectKeyFromEntry returns the project key from the first parseable access resource of the
// entry. It is a convenience wrapper over projectKeyFromResource that handles the fact that
// an audit entry may have multiple accesses (e.g. multi-environment flag changes), each with
// its own resource string. All accesses for a flag share the same project, so we stop at the
// first match.
func projectKeyFromEntry(entry models.LDAuditEntry) string {
	for _, access := range entry.Accesses {
		if proj := projectKeyFromResource(access.Resource); proj != "" {
			return proj
		}
	}
	return ""
}

// parseResource parses a LaunchDarkly resource string of the form:
//
//	proj/<projectKey>:env/<envKey>:flag/<flagKey>
//
// and returns a map of segment type → value (e.g. {"proj": "my-project", "flag": "my-flag"}).
// Unknown segment types are included as-is so callers can inspect them if needed.
func parseResource(resource string) map[string]string {
	segments := make(map[string]string)
	for _, part := range strings.Split(resource, ":") {
		idx := strings.IndexByte(part, '/')
		if idx < 0 {
			continue
		}
		segments[part[:idx]] = part[idx+1:]
	}
	return segments
}

// projectKeyFromResource returns the "proj" segment value from a resource string, or "" if absent.
func projectKeyFromResource(resource string) string {
	return parseResource(resource)["proj"]
}

// flagKeyFromResource returns the "flag" segment value from a resource string, or "" if absent.
func flagKeyFromResource(resource string) string {
	return parseResource(resource)["flag"]
}

type flagDetailWithRef struct {
	ref    flagRef
	detail ldactivities.GetFlagDetailOutput
}

// fetchFlagDetails fetches all flag details in parallel using Temporal's workflow.Go goroutines.
//
// Concurrency model:
//   - workflow.NewWaitGroup / wg.Add(1) / wg.Done() mirror sync.WaitGroup but are deterministic
//     and replay-safe. wg.Wait(ctx) blocks the workflow coroutine until all goroutines finish.
//   - results and errs are pre-allocated slices (make([]T, len(refs))). Each goroutine writes to
//     its own index (results[i], errs[i]), so there are no data races — no mutex needed.
//   - workflow.Go goroutines are cooperative (not OS threads), so "parallel" means interleaved
//     on Temporal's event loop; true parallelism on the worker happens at the activity layer.
//
// Error handling:
//   - Non-retryable errors (e.g. flag deleted — HTTP 404 → ApplicationError{NonRetryable: true})
//     are skipped. These are still visible in Temporal's event history for the activity task.
//   - Retryable errors are retried by Temporal before .Get() returns. If all retries are
//     exhausted, the error arrives here and is also skipped with a warning log.
func fetchFlagDetails(ctx workflow.Context, integration models.Integration, refs []flagRef) []flagDetailWithRef {
	logger := workflow.GetLogger(ctx)

	// Pre-allocate by index so each goroutine writes to its own slot — no synchronisation needed.
	results := make([]flagDetailWithRef, len(refs))
	errs := make([]error, len(refs))
	wg := workflow.NewWaitGroup(ctx)

	flagCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: flagDetailTimeout,
	})

	for i, ref := range refs {
		i, ref := i, ref // capture loop variables for the goroutine closure
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
			if err != nil {
				errs[i] = err // safe: pre-allocated index, each goroutine owns its slot
				return
			}
			results[i] = flagDetailWithRef{ref: ref, detail: out} // safe: same reason
		})
	}
	wg.Wait(ctx)

	var details []flagDetailWithRef
	for i, r := range results {
		if errs[i] != nil {
			// Log the skip so it is visible in Temporal's workflow history and worker logs.
			// The activity failure itself is already recorded in Temporal's event history,
			// so operators can inspect the exact error there. We log here to make the
			// downstream skip decision explicit.
			logger.Warn("skipping flag detail: activity failed after retries",
				"projectKey", refs[i].ProjectKey,
				"flagKey", refs[i].FlagKey,
				"error", errs[i],
			)
			continue
		}
		details = append(details, r)
	}
	return details
}

// spawnChangelogTransform fires a ChangelogTransformWorkflow and waits for it to start.
func spawnChangelogTransform(ctx workflow.Context, integration models.Integration, entry models.LDAuditEntry, projectKey string) error {
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        fmt.Sprintf("ld-changelog-%s-%s", integration.Key, entry.ID),
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	})
	input := ChangelogTransformInput{
		Entry:          entry,
		IntegrationKey: integration.Key,
		OrgID:          integration.OrgID,
		ProjectKey:     projectKey,
	}
	if err := workflow.ExecuteChildWorkflow(childCtx, LaunchDarklyChangelogTransformWorkflow, input).GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: start changelog transform for %s: %w", entry.ID, err)
	}
	return nil
}

// spawnFlagTransform fires a FlagTransformWorkflow and waits for it to start.
func spawnFlagTransform(ctx workflow.Context, integration models.Integration, projectKey string, d ldactivities.GetFlagDetailOutput) error {
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        fmt.Sprintf("ld-flag-%s-%s", integration.Key, d.Flag.Key),
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	})
	input := FlagTransformInput{
		Flag:            d.Flag,
		SourceRecordKey: d.SourceRecordKey,
		IntegrationKey:  integration.Key,
		OrgID:           integration.OrgID,
		ProjectKey:      projectKey,
	}
	if err := workflow.ExecuteChildWorkflow(childCtx, LaunchDarklyFlagTransformWorkflow, input).GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		return fmt.Errorf("LaunchDarklyExtractWorkflow: start flag transform for %s: %w", d.Flag.Key, err)
	}
	return nil
}
