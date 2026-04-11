package launchdarkly

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/datagill/datagill-core/activities"
	ldactivities "github.com/datagill/datagill-core/activities/launchdarkly"
	"github.com/datagill/datagill-core/models"
)

const (
	scrollAuditLogsTimeout  = 10 * time.Minute
	flagDetailTimeout        = 30 * time.Second
	transformActivityTimeout = 30 * time.Second
	updateLastSyncedTimeout  = 10 * time.Second
)

// ExtractInput is the input for LaunchDarklyExtractWorkflow.
type ExtractInput struct {
	Integration models.Integration
}

// LaunchDarklyExtractWorkflow orchestrates the full ETL extract for one LaunchDarkly integration.
// It scrolls all audit log entries since the last sync (or all-time on first run), fetches flag
// details in parallel, transforms changelogs and flags as parallel activities, then records the
// high-water mark for the next incremental sync.
func LaunchDarklyExtractWorkflow(ctx workflow.Context, input ExtractInput) error {
	integration := input.Integration
	logger := workflow.GetLogger(ctx)

	// Capture current time before any I/O so the watermark is conservative.
	syncedAt := workflow.Now(ctx)

	// --- Step 1: scroll all audit log pages ---
	scrollInput := ldactivities.ScrollAuditLogsInput{
		IntegrationKey: integration.Key,
		OrgID:          integration.OrgID,
	}
	if integration.LastSyncedAt != nil {
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

	// --- Step 4: transform changelogs in parallel ---
	transformCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: transformActivityTimeout,
	})
	changelogWg := workflow.NewWaitGroup(ctx)
	for _, entry := range scrollOut.Entries {
		if entry.Kind != "flag" {
			continue
		}
		projKey := projectKeyFromEntry(entry)
		if projKey == "" {
			logger.Warn("skipping flag entry: could not extract project key from resources",
				"entryID", entry.ID,
				"accesses", entry.Accesses,
			)
			continue
		}
		entry, projKey := entry, projKey
		changelogWg.Add(1)
		workflow.Go(ctx, func(gCtx workflow.Context) {
			defer changelogWg.Done()
			var changelog models.FeatureFlagChangelog
			if err := workflow.ExecuteActivity(transformCtx, (*ldactivities.Activities).NormalizeChangelog, ldactivities.NormalizeChangelogInput{
				Entry:          entry,
				IntegrationKey: integration.Key,
				OrgID:          integration.OrgID,
				ProjectKey:     projKey,
			}).Get(gCtx, &changelog); err != nil {
				logger.Warn("skipping changelog: normalize failed", "entryID", entry.ID, "error", err)
				return
			}
			if err := workflow.ExecuteActivity(transformCtx, (*activities.SharedActivities).SaveChangelog, changelog).Get(gCtx, nil); err != nil {
				logger.Warn("skipping changelog: save failed", "entryID", entry.ID, "error", err)
			}
		})
	}
	changelogWg.Wait(ctx)

	// --- Step 5: transform flags in parallel ---
	flagWg := workflow.NewWaitGroup(ctx)
	for _, d := range details {
		d := d
		flagWg.Add(1)
		workflow.Go(ctx, func(gCtx workflow.Context) {
			defer flagWg.Done()
			var featureFlag models.FeatureFlag
			if err := workflow.ExecuteActivity(transformCtx, (*ldactivities.Activities).NormalizeFlagDetail, ldactivities.NormalizeFlagDetailInput{
				Flag:            d.detail.Flag,
				SourceRecordKey: d.detail.SourceRecordKey,
				IntegrationKey:  integration.Key,
				OrgID:           integration.OrgID,
				ProjectKey:      d.ref.ProjectKey,
			}).Get(gCtx, &featureFlag); err != nil {
				logger.Warn("skipping flag: normalize failed", "flagKey", d.ref.FlagKey, "error", err)
				return
			}
			if err := workflow.ExecuteActivity(transformCtx, (*activities.SharedActivities).SaveFeatureFlag, featureFlag).Get(gCtx, nil); err != nil {
				logger.Warn("skipping flag: save failed", "flagKey", d.ref.FlagKey, "error", err)
			}
		})
	}
	flagWg.Wait(ctx)

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

// projectKeyFromEntry returns the project key from the first parseable access resource of the entry.
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
// and returns a map of segment type → value.
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

// fetchFlagDetails fetches all flag details in parallel using workflow.Go goroutines.
func fetchFlagDetails(ctx workflow.Context, integration models.Integration, refs []flagRef) []flagDetailWithRef {
	logger := workflow.GetLogger(ctx)

	results := make([]flagDetailWithRef, len(refs))
	errs := make([]error, len(refs))
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
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = flagDetailWithRef{ref: ref, detail: out}
		})
	}
	wg.Wait(ctx)

	var details []flagDetailWithRef
	for i, r := range results {
		if errs[i] != nil {
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
