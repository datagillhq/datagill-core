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
// so the key is scoped to source + project + flag only — not per environment.
func ComputeFlagKey(source Source, projectKey, sourceFlagKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s", source, projectKey, sourceFlagKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}

// ComputeSourceRecordKey returns a deterministic UUID v5 for a source record.
// The same raw API response always produces the same key, enabling safe re-ingestion.
func ComputeSourceRecordKey(source Source, sourceType SourceType, sourceKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s", source, sourceType, sourceKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}

// ComputeChangelogKey returns a deterministic UUID v5 for a changelog entry.
// sourceKey is the provider's audit event ID, which is unique within a project.
func ComputeChangelogKey(source Source, projectKey, sourceKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s", source, projectKey, sourceKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}
