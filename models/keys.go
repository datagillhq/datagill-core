package models

import (
	"fmt"

	"github.com/google/uuid"
)

// DataGillNamespace is the fixed UUID v5 namespace for all deterministic key computation.
// Never change this value — it would invalidate all existing keys.
var DataGillNamespace = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

// ComputeFlagKey returns a deterministic UUID v5 for a feature flag record.
// All environments for a flag are stored in the same row (in data.environments),
// so the key is scoped to org + source + project + flag only — not per environment.
// org_id is included to prevent collisions across tenants with the same source/project/flag.
func ComputeFlagKey(orgID uuid.UUID, source Source, projectKey, sourceFlagKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s|%s", orgID, source, projectKey, sourceFlagKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}

// ComputeSourceRecordKey returns a deterministic UUID v5 for a source record.
// The same raw API response always produces the same key, enabling safe re-ingestion.
// org_id is included to prevent collisions across tenants with the same source/type/key.
func ComputeSourceRecordKey(orgID uuid.UUID, source Source, sourceType SourceType, sourceKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s|%s", orgID, source, sourceType, sourceKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}

// ComputeChangelogKey returns a deterministic UUID v5 for a changelog entry.
// sourceKey is the provider's audit event ID, which is unique within a project.
// org_id is included to prevent collisions across tenants with the same source/project/event.
func ComputeChangelogKey(orgID uuid.UUID, source Source, projectKey, sourceKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s|%s", orgID, source, projectKey, sourceKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}

// ComputePullRequestKey returns a deterministic UUID v5 for a pull request record.
// sourcePRKey is GitHub's globally unique pull_request.id.
// org_id remains part of the key so DataGill never relies on cross-tenant global identity.
func ComputePullRequestKey(orgID uuid.UUID, sourcePRKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s", orgID, SourceGitHub, sourcePRKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}
