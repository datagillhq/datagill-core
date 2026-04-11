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
	ProjectKey      string
}

// GetFlagDetail fetches a flag's full configuration and saves it as a source record.
func (a *Activities) GetFlagDetail(ctx context.Context, input GetFlagDetailInput) (GetFlagDetailOutput, error) {
	flag, err := a.client.GetFlagDetail(ctx, input.ProjectKey, input.FlagKey)
	if err != nil {
		return GetFlagDetailOutput{}, toServiceError(err)
	}

	data, err := json.Marshal(flag)
	if err != nil {
		return GetFlagDetailOutput{}, fmt.Errorf("marshal flag %s: %w", input.FlagKey, err)
	}

	// Source record key is based on flag key (not version) so we upsert the same row.
	sourceKey := fmt.Sprintf("%s/%s", input.ProjectKey, input.FlagKey)
	recordKey := models.ComputeSourceRecordKey(input.OrgID, models.SourceLaunchDarkly, models.SourceTypeFlagDetail, sourceKey)

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

	return GetFlagDetailOutput{Flag: flag, SourceRecordKey: recordKey, ProjectKey: input.ProjectKey}, nil
}
