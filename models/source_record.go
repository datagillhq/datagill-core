package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// SourceType distinguishes what kind of raw API response is stored in a source record.
type SourceType string

const (
	SourceTypeAuditLog   SourceType = "audit_log"
	SourceTypeFlagDetail SourceType = "flag_detail"
	SourceTypePRSearch   SourceType = "pr_search"
	SourceTypePRDetail   SourceType = "pr_detail"
)

// SourceRecord maps to the source_records table.
// This table is append-only — rows are never updated after insert.
type SourceRecord struct {
	ID            int64
	Key           uuid.UUID
	IntegrationID uuid.UUID
	OrgID         uuid.UUID
	SourceType    SourceType
	Data          json.RawMessage // full raw provider response, untouched
	CreatedAt     time.Time
}
