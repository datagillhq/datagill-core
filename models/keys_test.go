package models_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

var testOrgID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
var otherOrgID = uuid.MustParse("00000000-0000-0000-0000-000000000002")

func TestComputeFlagKey_Deterministic(t *testing.T) {
	a := models.ComputeFlagKey(testOrgID, models.SourceLaunchDarkly, "web-app", "enable-checkout-v2")
	b := models.ComputeFlagKey(testOrgID, models.SourceLaunchDarkly, "web-app", "enable-checkout-v2")
	if a != b {
		t.Errorf("same inputs produced different keys: %s vs %s", a, b)
	}
}

func TestComputeFlagKey_Unique(t *testing.T) {
	tests := []struct {
		name    string
		orgID   uuid.UUID
		source  models.Source
		project string
		flagKey string
	}{
		{"diff org", otherOrgID, models.SourceLaunchDarkly, "web-app", "enable-checkout-v2"},
		{"diff source", testOrgID, models.SourceUnleash, "web-app", "enable-checkout-v2"},
		{"diff project", testOrgID, models.SourceLaunchDarkly, "mobile-app", "enable-checkout-v2"},
		{"diff flag", testOrgID, models.SourceLaunchDarkly, "web-app", "enable-dark-mode"},
	}

	base := models.ComputeFlagKey(testOrgID, models.SourceLaunchDarkly, "web-app", "enable-checkout-v2")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := models.ComputeFlagKey(tt.orgID, tt.source, tt.project, tt.flagKey)
			if got == base {
				t.Errorf("expected different key for %q, got same as base", tt.name)
			}
		})
	}
}

func TestComputeSourceRecordKey_Deterministic(t *testing.T) {
	a := models.ComputeSourceRecordKey(testOrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, "evt-123")
	b := models.ComputeSourceRecordKey(testOrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, "evt-123")
	if a != b {
		t.Errorf("same inputs produced different keys: %s vs %s", a, b)
	}
}

func TestComputeSourceRecordKey_Unique(t *testing.T) {
	base := models.ComputeSourceRecordKey(testOrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, "evt-123")

	diffOrg := models.ComputeSourceRecordKey(otherOrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, "evt-123")
	if diffOrg == base {
		t.Error("different org should produce different key")
	}

	diffType := models.ComputeSourceRecordKey(testOrgID, models.SourceLaunchDarkly, models.SourceTypeFlagDetail, "evt-123")
	if diffType == base {
		t.Error("different source type should produce different key")
	}

	diffKey := models.ComputeSourceRecordKey(testOrgID, models.SourceLaunchDarkly, models.SourceTypeAuditLog, "evt-456")
	if diffKey == base {
		t.Error("different source key should produce different key")
	}
}

func TestComputeChangelogKey_Deterministic(t *testing.T) {
	a := models.ComputeChangelogKey(testOrgID, models.SourceLaunchDarkly, "web-app", "evt-abc")
	b := models.ComputeChangelogKey(testOrgID, models.SourceLaunchDarkly, "web-app", "evt-abc")
	if a != b {
		t.Errorf("same inputs produced different keys: %s vs %s", a, b)
	}
}

func TestComputeChangelogKey_DifferentAuditEvents(t *testing.T) {
	// Two different audit events must produce different changelog keys.
	// sourceKey is the provider's audit event ID, not the flag key.
	a := models.ComputeChangelogKey(testOrgID, models.SourceLaunchDarkly, "web-app", "evt-001")
	b := models.ComputeChangelogKey(testOrgID, models.SourceLaunchDarkly, "web-app", "evt-002")
	if a == b {
		t.Error("different audit event IDs should produce different changelog keys")
	}
}

func TestComputeChangelogKey_DifferentOrgs(t *testing.T) {
	// Same source/project/event across two orgs must produce different keys.
	a := models.ComputeChangelogKey(testOrgID, models.SourceLaunchDarkly, "web-app", "evt-001")
	b := models.ComputeChangelogKey(otherOrgID, models.SourceLaunchDarkly, "web-app", "evt-001")
	if a == b {
		t.Error("different orgs should produce different changelog keys")
	}
}
