package models

import (
	"fmt"

	"github.com/google/uuid"
)

// DataGillNamespace is the fixed UUID v5 namespace for all deterministic key computation.
// Never change this value — it would invalidate all existing keys.
var DataGillNamespace = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

// ComputeFlagKey returns a deterministic UUID v5 for a feature flag record.
// Inputs uniquely identify a flag within a provider project and environment.
func ComputeFlagKey(source Source, envKey, projectKey, sourceFlagKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s|%s", source, envKey, projectKey, sourceFlagKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}

// ComputeSourceRecordKey returns a deterministic UUID v5 for a source record.
// The same raw API response always produces the same key, enabling safe re-ingestion.
func ComputeSourceRecordKey(source Source, sourceType SourceType, sourceKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s", source, sourceType, sourceKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}

// ComputeChangelogKey returns a deterministic UUID v5 for a changelog entry.
// Inputs uniquely identify a single audit event from a provider.
func ComputeChangelogKey(source Source, envKey, projectKey, sourceKey string) uuid.UUID {
	raw := fmt.Sprintf("%s|%s|%s|%s", source, envKey, projectKey, sourceKey)
	return uuid.NewSHA1(DataGillNamespace, []byte(raw))
}
