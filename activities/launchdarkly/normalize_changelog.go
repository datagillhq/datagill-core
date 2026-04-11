package launchdarkly

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// NormalizeChangelogInput is the input for the NormalizeChangelog activity.
type NormalizeChangelogInput struct {
	Entry          models.LDAuditEntry
	IntegrationKey uuid.UUID
	OrgID          uuid.UUID
}

// NormalizeChangelog transforms a LaunchDarkly audit entry into a canonical changelog entry.
func (a *Activities) NormalizeChangelog(_ context.Context, input NormalizeChangelogInput) (models.FeatureFlagChangelog, error) {
	entry := input.Entry

	if entry.Kind != "flag" {
		return models.FeatureFlagChangelog{}, fmt.Errorf("unsupported audit entry kind: %s", entry.Kind)
	}

	flagKey := extractFlagKey(entry)
	if flagKey == "" {
		return models.FeatureFlagChangelog{}, fmt.Errorf("audit entry %s has no flag key in accesses", entry.ID)
	}

	projectKey := extractProjectKey(entry)
	if projectKey == "" {
		return models.FeatureFlagChangelog{}, fmt.Errorf("audit entry %s has no project key in accesses", entry.ID)
	}

	changelog := models.FeatureFlagChangelog{
		Key:             models.ComputeChangelogKey(input.OrgID, models.SourceLaunchDarkly, projectKey, entry.ID),
		SourceRecordKey: models.ComputeSourceRecordKey(input.OrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, entry.ID),
		FeatureFlagKey:  models.ComputeFlagKey(input.OrgID, models.SourceLaunchDarkly, projectKey, flagKey),
		IntegrationID:   input.IntegrationKey,
		OrgID:           input.OrgID,
		EventType:       mapActionToEventType(firstAction(entry)),
		State:           models.RecordStateActive,
		OccurredAt:      time.UnixMilli(entry.Date),
		Data: models.ChangelogData{
			Description:  entry.Description,
			Actor:        extractActor(entry),
			Environments: extractEnvironments(entry),
			ChangeType:   firstAction(entry),
			Source: models.FlagSource{
				Key:      flagKey,
				Provider: models.SourceLaunchDarkly,
				Project:  models.FlagProject{ID: projectKey},
			},
		},
	}

	return changelog, nil
}

// resourceSegment parses one segment of a colon-delimited resource string.
// "proj/my-project:env/production:flag/my-flag" with typ "flag" → "my-flag".
func resourceSegment(resource, typ string) string {
	for _, part := range strings.Split(resource, ":") {
		if before, after, ok := strings.Cut(part, "/"); ok && before == typ {
			return after
		}
	}
	return ""
}

func firstAction(entry models.LDAuditEntry) string {
	for _, access := range entry.Accesses {
		if access.Action != "" {
			return access.Action
		}
	}
	return ""
}

func extractFlagKey(entry models.LDAuditEntry) string {
	for _, access := range entry.Accesses {
		if key := resourceSegment(access.Resource, "flag"); key != "" {
			return key
		}
	}
	return ""
}

func extractEnvironments(entry models.LDAuditEntry) []string {
	seen := make(map[string]bool)
	var envs []string
	for _, access := range entry.Accesses {
		if env := resourceSegment(access.Resource, "env"); env != "" && !seen[env] {
			seen[env] = true
			envs = append(envs, env)
		}
	}
	return envs
}

func extractProjectKey(entry models.LDAuditEntry) string {
	for _, access := range entry.Accesses {
		if key := resourceSegment(access.Resource, "proj"); key != "" {
			return key
		}
	}
	return ""
}

func extractActor(entry models.LDAuditEntry) models.ChangelogActor {
	if entry.Member != nil {
		return models.ChangelogActor{Email: entry.Member.Email}
	}
	return models.ChangelogActor{}
}

// mapActionToEventType maps a LaunchDarkly flag action to a canonical event type.
// Actions are stable API enum values; see LaunchDarkly feature flag actions docs.
func mapActionToEventType(action string) models.EventType {
	switch action {
	case "createFlag", "cloneFlag":
		return models.EventTypeCreated
	case "deleteFlag":
		return models.EventTypeDeleted
	case "updateOn":
		return models.EventTypeToggled
	default:
		return models.EventTypeUpdated
	}
}

