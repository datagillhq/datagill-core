package launchdarkly

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// ListAuditLogsInput is the input for the ListAuditLogs activity.
// For the first call, set Before and/or After to bound the time range.
// For subsequent pages, set NextURL to the value from the previous output.
type ListAuditLogsInput struct {
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
	Before         time.Time // optional: only return entries before this time
	After          time.Time // optional: only return entries after this time
	NextURL        string    // optional: if set, fetches this page directly (overrides Before/After/Limit)
	Limit          int
}

// ListAuditLogsOutput is the output from the ListAuditLogs activity.
type ListAuditLogsOutput struct {
	Entries []models.LDAuditEntry
	NextURL string // non-empty means more pages exist; pass as input.NextURL in the next call
	HasMore bool
}

// ListAuditLogs fetches one page of audit log entries from LaunchDarkly and saves them as source records.
// When output.HasMore is true, pass output.NextURL as input.NextURL in the next call to get the next page.
func (a *Activities) ListAuditLogs(ctx context.Context, input ListAuditLogsInput) (ListAuditLogsOutput, error) {
	entries, nextURL, err := a.fetchPage(ctx, input)
	if err != nil {
		return ListAuditLogsOutput{}, toServiceError(err)
	}

	output := ListAuditLogsOutput{
		Entries: entries,
		NextURL: nextURL,
		HasMore: nextURL != "",
	}

	for _, entry := range entries {
		if err := a.saveAuditEntry(ctx, entry, input); err != nil {
			return ListAuditLogsOutput{}, err
		}
	}

	return output, nil
}

// ScrollAuditLogsInput is the input for the ScrollAuditLogs activity.
// ScrollAuditLogs returns all entries within the given time range — no limit needed.
type ScrollAuditLogsInput struct {
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
	Before         time.Time // optional upper bound
	After          time.Time // optional lower bound
}

// ScrollAuditLogsOutput is the output from the ScrollAuditLogs activity.
type ScrollAuditLogsOutput struct {
	Entries []models.LDAuditEntry
}

// ScrollAuditLogs fetches all pages of audit log entries within the given time range,
// saves each entry as a source record, and returns all entries concatenated.
// It calls ListAuditLogs in a loop, following NextURL until HasMore is false.
func (a *Activities) ScrollAuditLogs(ctx context.Context, input ScrollAuditLogsInput) (ScrollAuditLogsOutput, error) {
	var allEntries []models.LDAuditEntry

	pageInput := ListAuditLogsInput{
		IntegrationKey: input.IntegrationKey,
		OrgID:          input.OrgID,
		Before:         input.Before,
		After:          input.After,
	}

	for {
		out, err := a.ListAuditLogs(ctx, pageInput)
		if err != nil {
			return ScrollAuditLogsOutput{}, err
		}

		allEntries = append(allEntries, out.Entries...)

		if !out.HasMore {
			break
		}

		pageInput = ListAuditLogsInput{
			IntegrationKey: input.IntegrationKey,
			OrgID:          input.OrgID,
			NextURL:        out.NextURL,
		}
	}

	return ScrollAuditLogsOutput{Entries: allEntries}, nil
}

func (a *Activities) fetchPage(ctx context.Context, input ListAuditLogsInput) ([]models.LDAuditEntry, string, error) {
	if input.NextURL != "" {
		return a.client.ListAuditLogPage(ctx, input.NextURL)
	}
	return a.client.ListAuditLog(ctx, input.Before, input.After, input.Limit)
}

func (a *Activities) saveAuditEntry(ctx context.Context, entry models.LDAuditEntry, input ListAuditLogsInput) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry %s: %w", entry.ID, err)
	}

	record := models.SourceRecord{
		Key:           models.ComputeSourceRecordKey(input.OrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, entry.ID),
		IntegrationID: input.IntegrationKey,
		OrgID:         input.OrgID,
		SourceType:    models.SourceTypeAuditLog,
		Data:          data,
	}

	if err := a.repo.SaveSourceRecord(ctx, record); err != nil {
		return fmt.Errorf("save source record %s: %w", entry.ID, err)
	}

	return nil
}
