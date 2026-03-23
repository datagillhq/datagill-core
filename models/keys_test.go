package models_test

import (
	"testing"

	"github.com/datagill/datagill-core/models"
)

func TestComputeFlagKey_Deterministic(t *testing.T) {
	a := models.ComputeFlagKey(models.SourceLaunchDarkly, "web-app", "enable-checkout-v2")
	b := models.ComputeFlagKey(models.SourceLaunchDarkly, "web-app", "enable-checkout-v2")
	if a != b {
		t.Errorf("same inputs produced different keys: %s vs %s", a, b)
	}
}

func TestComputeFlagKey_Unique(t *testing.T) {
	tests := []struct {
		name    string
		source  models.Source
		project string
		flagKey string
	}{
		{"diff source", models.SourceUnleash, "web-app", "enable-checkout-v2"},
		{"diff project", models.SourceLaunchDarkly, "mobile-app", "enable-checkout-v2"},
		{"diff flag", models.SourceLaunchDarkly, "web-app", "enable-dark-mode"},
	}

	base := models.ComputeFlagKey(models.SourceLaunchDarkly, "web-app", "enable-checkout-v2")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := models.ComputeFlagKey(tt.source, tt.project, tt.flagKey)
			if got == base {
				t.Errorf("expected different key for %q, got same as base", tt.name)
			}
		})
	}
}

func TestComputeSourceRecordKey_Deterministic(t *testing.T) {
	a := models.ComputeSourceRecordKey(models.SourceLaunchDarkly, models.SourceTypeAuditLog, "evt-123")
	b := models.ComputeSourceRecordKey(models.SourceLaunchDarkly, models.SourceTypeAuditLog, "evt-123")
	if a != b {
		t.Errorf("same inputs produced different keys: %s vs %s", a, b)
	}
}

func TestComputeSourceRecordKey_Unique(t *testing.T) {
	base := models.ComputeSourceRecordKey(models.SourceLaunchDarkly, models.SourceTypeAuditLog, "evt-123")

	diffType := models.ComputeSourceRecordKey(models.SourceLaunchDarkly, models.SourceTypeFlagDetail, "evt-123")
	if diffType == base {
		t.Error("different source type should produce different key")
	}

	diffKey := models.ComputeSourceRecordKey(models.SourceLaunchDarkly, models.SourceTypeAuditLog, "evt-456")
	if diffKey == base {
		t.Error("different source key should produce different key")
	}
}

func TestComputeChangelogKey_Deterministic(t *testing.T) {
	a := models.ComputeChangelogKey(models.SourceLaunchDarkly, "web-app", "evt-abc")
	b := models.ComputeChangelogKey(models.SourceLaunchDarkly, "web-app", "evt-abc")
	if a != b {
		t.Errorf("same inputs produced different keys: %s vs %s", a, b)
	}
}

func TestComputeChangelogKey_DifferentAuditEvents(t *testing.T) {
	// Two different audit events must produce different changelog keys.
	// sourceKey is the provider's audit event ID, not the flag key.
	a := models.ComputeChangelogKey(models.SourceLaunchDarkly, "web-app", "evt-001")
	b := models.ComputeChangelogKey(models.SourceLaunchDarkly, "web-app", "evt-002")
	if a == b {
		t.Error("different audit event IDs should produce different changelog keys")
	}
}
