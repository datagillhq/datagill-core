package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
)

// LaunchDarklyActivities contains Temporal activities for LaunchDarkly ETL.
type LaunchDarklyActivities struct {
	client *LaunchDarklyClient
	repo   repository.Repository
}

// NewLaunchDarklyActivities creates a new LaunchDarklyActivities instance.
func NewLaunchDarklyActivities(client *LaunchDarklyClient, repo repository.Repository) *LaunchDarklyActivities {
	return &LaunchDarklyActivities{
		client: client,
		repo:   repo,
	}
}

// ListAuditLogsInput is the input for ListAuditLogs activity.
type ListAuditLogsInput struct {
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
	ProjectKey     string
	Before         time.Time
	Limit          int
}

// ListAuditLogsOutput is the output from ListAuditLogs activity.
type ListAuditLogsOutput struct {
	Entries      []models.LDAuditEntry
	OldestDate   time.Time // For pagination: use as "before" in next call
	HasMore      bool
	RecordsSaved int
}

// ListAuditLogs fetches audit log entries from LaunchDarkly and saves them as source records.
// Returns the entries for downstream processing.
func (a *LaunchDarklyActivities) ListAuditLogs(ctx context.Context, input ListAuditLogsInput) (ListAuditLogsOutput, error) {
	entries, err := a.client.ListAuditLog(ctx, input.Before, input.Limit)
	if err != nil {
		return ListAuditLogsOutput{}, fmt.Errorf("list audit log: %w", err)
	}

	var output ListAuditLogsOutput
	output.Entries = entries
	output.HasMore = len(entries) == input.Limit

	// Save each entry as a source record
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return ListAuditLogsOutput{}, fmt.Errorf("marshal audit entry %s: %w", entry.ID, err)
		}

		record := models.SourceRecord{
			Key:           models.ComputeSourceRecordKey(input.OrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, entry.ID),
			IntegrationID: input.IntegrationKey,
			OrgID:         input.OrgID,
			SourceType:    models.SourceTypeAuditLog,
			Data:          data,
		}

		if err := a.repo.SaveSourceRecord(ctx, record); err != nil {
			return ListAuditLogsOutput{}, fmt.Errorf("save source record %s: %w", entry.ID, err)
		}
		output.RecordsSaved++

		// Track oldest date for pagination
		entryTime := time.UnixMilli(entry.Date)
		if output.OldestDate.IsZero() || entryTime.Before(output.OldestDate) {
			output.OldestDate = entryTime
		}
	}

	return output, nil
}

// GetFlagDetailInput is the input for GetFlagDetail activity.
type GetFlagDetailInput struct {
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
	ProjectKey     string
	FlagKey        string
}

// GetFlagDetailOutput is the output from GetFlagDetail activity.
type GetFlagDetailOutput struct {
	Flag            models.LDFlagDetail
	SourceRecordKey uuid.UUID
}

// GetFlagDetail fetches a flag's full configuration and saves it as a source record.
func (a *LaunchDarklyActivities) GetFlagDetail(ctx context.Context, input GetFlagDetailInput) (GetFlagDetailOutput, error) {
	flag, err := a.client.GetFlagDetail(ctx, input.ProjectKey, input.FlagKey)
	if err != nil {
		return GetFlagDetailOutput{}, fmt.Errorf("get flag detail %s/%s: %w", input.ProjectKey, input.FlagKey, err)
	}

	data, err := json.Marshal(flag)
	if err != nil {
		return GetFlagDetailOutput{}, fmt.Errorf("marshal flag %s: %w", input.FlagKey, err)
	}

	// Source record key is based on flag key (not version) so we upsert the same row
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

	return GetFlagDetailOutput{
		Flag:            flag,
		SourceRecordKey: recordKey,
	}, nil
}

// NormalizeChangelogInput is the input for NormalizeChangelog activity.
type NormalizeChangelogInput struct {
	Entry          models.LDAuditEntry
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
	ProjectKey     string
}

// NormalizeChangelog transforms a LaunchDarkly audit entry into a canonical changelog entry.
func (a *LaunchDarklyActivities) NormalizeChangelog(ctx context.Context, input NormalizeChangelogInput) (models.FeatureFlagChangelog, error) {
	entry := input.Entry

	// Only process flag-related audit entries
	if entry.Kind != "flag" {
		return models.FeatureFlagChangelog{}, fmt.Errorf("unsupported audit entry kind: %s", entry.Kind)
	}

	// Extract flag key from the entry
	flagKey := entry.Name
	if flagKey == "" {
		return models.FeatureFlagChangelog{}, fmt.Errorf("audit entry %s has no flag name", entry.ID)
	}

	// Compute deterministic keys
	changelogKey := models.ComputeChangelogKey(input.OrgID, models.SourceLaunchDarkly, input.ProjectKey, entry.ID)
	sourceRecordKey := models.ComputeSourceRecordKey(input.OrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, entry.ID)
	featureFlagKey := models.ComputeFlagKey(input.OrgID, models.SourceLaunchDarkly, input.ProjectKey, flagKey)

	// Map TitleVerb to EventType
	eventType := mapTitleVerbToEventType(entry.TitleVerb)

	// Extract actor information
	actor := extractActor(entry)

	// Extract environments from accesses
	environments := extractEnvironments(entry)

	changelog := models.FeatureFlagChangelog{
		Key:             changelogKey,
		SourceRecordKey: sourceRecordKey,
		FeatureFlagKey:  featureFlagKey,
		IntegrationID:   input.IntegrationKey,
		OrgID:           input.OrgID,
		EventType:       eventType,
		State:           inferStateFromEntry(entry),
		OccurredAt:      time.UnixMilli(entry.Date),
		Data: models.ChangelogData{
			Description:  entry.Description,
			Actor:        actor,
			Environments: environments,
			ChangeType:   entry.TitleVerb,
			Source: models.FlagSource{
				Key:      flagKey,
				Provider: models.SourceLaunchDarkly,
				Project:  models.FlagProject{ID: input.ProjectKey},
			},
		},
	}

	return changelog, nil
}

// NormalizeFlagDetailInput is the input for NormalizeFlagDetail activity.
type NormalizeFlagDetailInput struct {
	Flag            models.LDFlagDetail
	SourceRecordKey uuid.UUID
	IntegrationKey  uuid.UUID
	OrgID           uuid.UUID
	ProjectKey      string
}

// NormalizeFlagDetail transforms a LaunchDarkly flag into a canonical feature flag.
func (a *LaunchDarklyActivities) NormalizeFlagDetail(ctx context.Context, input NormalizeFlagDetailInput) (models.FeatureFlag, error) {
	flag := input.Flag

	// Compute deterministic key
	flagKey := models.ComputeFlagKey(input.OrgID, models.SourceLaunchDarkly, input.ProjectKey, flag.Key)

	// Compute overall state from environments
	state := computeFlagState(flag)

	// Map environments
	environments := make(map[string]models.FlagEnvironment)
	for envKey, envConfig := range flag.Environments {
		environments[envKey] = models.FlagEnvironment{
			Enabled: envConfig.On,
		}
	}

	// Extract maintainer info
	var maintainer *models.FlagMaintainer
	if flag.Maintainer != nil && flag.Maintainer.Email != "" {
		maintainer = &models.FlagMaintainer{
			Person: &models.FlagMaintainerPerson{
				Email: flag.Maintainer.Email,
			},
		}
	} else if flag.MaintainerTeam != nil && flag.MaintainerTeam.Name != "" {
		maintainer = &models.FlagMaintainer{
			Team: &models.FlagMaintainerTeam{
				Name: flag.MaintainerTeam.Name,
			},
		}
	}

	// Map lifecycle
	lifecycle := models.FlagLifecycle{
		Purpose: mapTemporaryToPurpose(flag.Temporary),
		State:   mapFlagToLifecycleState(flag),
	}

	// Creation date
	var createdAt *time.Time
	if flag.CreationDate > 0 {
		t := time.UnixMilli(flag.CreationDate)
		createdAt = &t
	}

	featureFlag := models.FeatureFlag{
		Key:             flagKey,
		SourceRecordKey: input.SourceRecordKey,
		IntegrationID:   input.IntegrationKey,
		OrgID:           input.OrgID,
		State:           state,
		Data: models.FeatureFlagData{
			Name:        flag.Name,
			Description: flag.Description,
			Source: models.FlagSource{
				Key:       flag.Key,
				Provider:  models.SourceLaunchDarkly,
				Project:   models.FlagProject{ID: input.ProjectKey},
				CreatedAt: createdAt,
			},
			Maintainer:   maintainer,
			Lifecycle:    lifecycle,
			Environments: environments,
		},
	}

	return featureFlag, nil
}

// mapTitleVerbToEventType maps LaunchDarkly's titleVerb to canonical EventType.
func mapTitleVerbToEventType(titleVerb string) models.EventType {
	verb := strings.ToLower(titleVerb)

	switch {
	case verb == "created":
		return models.EventTypeCreated
	case verb == "deleted":
		return models.EventTypeDeleted
	case strings.Contains(verb, "turned targeting"):
		return models.EventTypeToggled
	case strings.Contains(verb, "rollback"):
		return models.EventTypeRollback
	default:
		// updated, changed, added, removed, etc.
		return models.EventTypeUpdated
	}
}

// extractActor extracts actor information from an audit entry.
func extractActor(entry models.LDAuditEntry) models.ChangelogActor {
	var actor models.ChangelogActor

	if entry.Member != nil && entry.Member.Email != "" {
		actor.Email = entry.Member.Email
	}

	return actor
}

// extractEnvironments extracts environment keys from audit entry accesses.
func extractEnvironments(entry models.LDAuditEntry) []string {
	var environments []string
	seen := make(map[string]bool)

	for _, access := range entry.Accesses {
		// Resource format: "proj/<project>:env/<env>:flag/<flag>"
		parts := strings.Split(access.Resource, ":")
		for _, part := range parts {
			if strings.HasPrefix(part, "env/") {
				env := strings.TrimPrefix(part, "env/")
				if !seen[env] {
					seen[env] = true
					environments = append(environments, env)
				}
			}
		}
	}

	return environments
}

// inferStateFromEntry infers flag state from an audit entry.
func inferStateFromEntry(entry models.LDAuditEntry) models.FlagState {
	verb := strings.ToLower(entry.TitleVerb)

	if strings.Contains(verb, "turned targeting on") {
		return models.FlagStateOn
	}
	if strings.Contains(verb, "turned targeting off") {
		return models.FlagStateOff
	}

	// Default to partial for other changes (rules, targeting, etc.)
	return models.FlagStatePartial
}

// computeFlagState determines the overall state from environment configs.
func computeFlagState(flag models.LDFlagDetail) models.FlagState {
	if len(flag.Environments) == 0 {
		return models.FlagStateOff
	}

	allOn := true
	allOff := true

	for _, env := range flag.Environments {
		if env.On {
			allOff = false
		} else {
			allOn = false
		}
	}

	if allOn {
		return models.FlagStateOn
	}
	if allOff {
		return models.FlagStateOff
	}
	return models.FlagStatePartial
}

// mapTemporaryToPurpose maps the temporary flag to lifecycle purpose.
func mapTemporaryToPurpose(temporary bool) models.LifecyclePurpose {
	if temporary {
		return models.LifecyclePurposeTemporary
	}
	return models.LifecyclePurposePermanent
}

// mapFlagToLifecycleState determines lifecycle state from flag metadata.
func mapFlagToLifecycleState(flag models.LDFlagDetail) models.LifecycleState {
	if flag.Archived {
		return models.LifecycleStateArchived
	}
	if flag.Deprecated {
		return models.LifecycleStateDeprecated
	}
	return models.LifecycleStateActive
}
