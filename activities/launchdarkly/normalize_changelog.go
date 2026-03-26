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
	ProjectKey     string
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

	changelog := models.FeatureFlagChangelog{
		Key:             models.ComputeChangelogKey(input.OrgID, models.SourceLaunchDarkly, input.ProjectKey, entry.ID),
		SourceRecordKey: models.ComputeSourceRecordKey(input.OrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, entry.ID),
		FeatureFlagKey:  models.ComputeFlagKey(input.OrgID, models.SourceLaunchDarkly, input.ProjectKey, flagKey),
		IntegrationID:   input.IntegrationKey,
		OrgID:           input.OrgID,
		EventType:       mapTitleVerbToEventType(entry.TitleVerb),
		State:           inferStateFromEntry(entry),
		OccurredAt:      time.UnixMilli(entry.Date),
		Data: models.ChangelogData{
			Description:  entry.Description,
			Actor:        extractActor(entry),
			Environments: extractEnvironments(entry),
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
		return models.EventTypeUpdated
	}
}

func extractFlagKey(entry models.LDAuditEntry) string {
	for _, access := range entry.Accesses {
		for _, part := range strings.Split(access.Resource, ":") {
			if key, ok := strings.CutPrefix(part, "flag/"); ok && key != "" {
				return key
			}
		}
	}
	return ""
}

func extractActor(entry models.LDAuditEntry) models.ChangelogActor {
	if entry.Member != nil {
		return models.ChangelogActor{Email: entry.Member.Email}
	}
	if entry.Token != nil {
		// Service token or personal access token; no email available.
		return models.ChangelogActor{Email: "token:" + entry.Token.Name}
	}
	return models.ChangelogActor{}
}

func extractEnvironments(entry models.LDAuditEntry) []string {
	seen := make(map[string]bool)
	var envs []string

	for _, access := range entry.Accesses {
		for _, part := range strings.Split(access.Resource, ":") {
			if env, ok := strings.CutPrefix(part, "env/"); ok && !seen[env] {
				seen[env] = true
				envs = append(envs, env)
			}
		}
	}

	return envs
}

func inferStateFromEntry(entry models.LDAuditEntry) models.FlagState {
	verb := strings.ToLower(entry.TitleVerb)
	switch {
	case strings.Contains(verb, "turned targeting on"):
		return models.FlagStateOn
	case strings.Contains(verb, "turned targeting off"):
		return models.FlagStateOff
	default:
		return models.FlagStatePartial
	}
}
