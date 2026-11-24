package launchdarkly

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// GetFlagDetailInput is the input for the GetFlagDetail activity.
type GetFlagDetailInput struct {
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
	ProjectKey     string
	FlagKey        string
}

// GetFlagDetailOutput is the output from the GetFlagDetail activity.
type GetFlagDetailOutput struct {
	Flag            models.LDFlagDetail
	SourceRecordKey uuid.UUID
}

// GetFlagDetail fetches a flag's full configuration and saves it as a source record.
func (a *Activities) GetFlagDetail(ctx context.Context, input GetFlagDetailInput) (GetFlagDetailOutput, error) {
	c, err := a.clientFor(ctx, input.OrgID, input.IntegrationKey)
	if err != nil {
		return GetFlagDetailOutput{}, err
	}
	flag, err := c.GetFlagDetail(ctx, input.ProjectKey, input.FlagKey)
	if err != nil {
		return GetFlagDetailOutput{}, toServiceError(err)
	}

	data, err := json.Marshal(flag)
	if err != nil {
		return GetFlagDetailOutput{}, fmt.Errorf("marshal flag %s: %w", input.FlagKey, err)
	}

	recordKey := flagDetailRecordKey(input.OrgID, input.ProjectKey, input.FlagKey, flag.Version)
	sourceKey := fmt.Sprintf("%s/%s", input.ProjectKey, input.FlagKey)

	record := models.SourceRecord{
		Key:           recordKey,
		IntegrationID: input.IntegrationKey,
		OrgID:         input.OrgID,
		SourceType:    models.SourceTypeFlagDetail,
		Data:          data,
	}

	if err := a.repo.SaveSourceRecord(ctx, record); err != nil {
		return GetFlagDetailOutput{}, fmt.Errorf("save source record %s: %w", sourceKey, err)
	}

	return GetFlagDetailOutput{Flag: flag, SourceRecordKey: recordKey}, nil
}

// flagDetailRecordKey returns the source record key for one version of a flag.
//
// source_records is append-only (a key that already exists is not overwritten), so the
// key must change whenever the flag does. Keying by flag alone kept only the first fetch:
// later versions were never stored raw, and replaying source_records rebuilt the flag as
// it was on day one. LaunchDarkly bumps _version on every change, and a re-fetch of an
// unchanged flag maps to the existing row.
func flagDetailRecordKey(orgID uuid.UUID, projectKey, flagKey string, version int) uuid.UUID {
	sourceKey := fmt.Sprintf("%s/%s@v%d", projectKey, flagKey, version)
	return models.ComputeSourceRecordKey(orgID, models.SourceLaunchDarkly, models.SourceTypeFlagDetail, sourceKey)
}
