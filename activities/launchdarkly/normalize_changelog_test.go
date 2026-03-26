package launchdarkly

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

func TestNormalizeChangelog_Created(t *testing.T) {
	entry := models.LDAuditEntry{
		ID:   "audit-123",
		Kind: "flag",
		Date: time.Now().UnixMilli(),
		Accesses: []models.LDAccess{
			{Action: "createFlag", Resource: "proj/my-project:env/production:flag/new-feature"},
		},
		Member: &models.LDAuditMember{Email: "dev@example.com"},
	}

	acts := New(mustNewClient(t, "test-key"), newMockRepository())
	out, err := acts.NormalizeChangelog(context.Background(), NormalizeChangelogInput{
		Entry:          entry,
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
	})
	if err != nil {
		t.Fatalf("NormalizeChangelog failed: %v", err)
	}
	if out.EventType != models.EventTypeCreated {
		t.Errorf("expected EventType 'created', got %q", out.EventType)
	}
	if out.Data.ChangeType != "createFlag" {
		t.Errorf("expected ChangeType 'createFlag', got %q", out.Data.ChangeType)
	}
	if out.Data.Actor.Email != "dev@example.com" {
		t.Errorf("expected actor email 'dev@example.com', got %q", out.Data.Actor.Email)
	}
	if len(out.Data.Environments) != 1 || out.Data.Environments[0] != "production" {
		t.Errorf("expected environments [production], got %v", out.Data.Environments)
	}
}

func TestNormalizeChangelog_Toggled(t *testing.T) {
	tests := []struct {
		titleVerb     string
		expectedState models.FlagState
	}{
		{"turned targeting on", models.FlagStateOn},
		{"turned targeting off", models.FlagStateOff},
	}

	acts := New(mustNewClient(t, "test-key"), newMockRepository())

	for _, tt := range tests {
		t.Run(tt.titleVerb, func(t *testing.T) {
			out, err := acts.NormalizeChangelog(context.Background(), NormalizeChangelogInput{
				Entry: models.LDAuditEntry{
					ID:        "audit-456",
					Kind:      "flag",
					Date:      time.Now().UnixMilli(),
					TitleVerb: tt.titleVerb,
					Accesses: []models.LDAccess{
						{Action: "updateOn", Resource: "proj/my-project:env/production:flag/toggle-flag"},
					},
				},
				IntegrationKey: uuid.New(),
				OrgID:          uuid.New(),
				ProjectKey:     "my-project",
			})
			if err != nil {
				t.Fatalf("NormalizeChangelog failed: %v", err)
			}
			if out.EventType != models.EventTypeToggled {
				t.Errorf("expected EventType 'toggled', got %q", out.EventType)
			}
			if out.State != tt.expectedState {
				t.Errorf("expected state %q, got %q", tt.expectedState, out.State)
			}
		})
	}
}

func TestNormalizeChangelog_NonFlagKind(t *testing.T) {
	acts := New(mustNewClient(t, "test-key"), newMockRepository())
	_, err := acts.NormalizeChangelog(context.Background(), NormalizeChangelogInput{
		Entry: models.LDAuditEntry{
			ID:   "audit-789",
			Kind: "environment",
			Date: time.Now().UnixMilli(),
		},
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
	})
	if err == nil {
		t.Fatal("expected error for non-flag kind")
	}
}

func TestNormalizeChangelog_TokenActor(t *testing.T) {
	entry := models.LDAuditEntry{
		ID:   "audit-token",
		Kind: "flag",
		Date: time.Now().UnixMilli(),
		Token: &models.LDAuditToken{Name: "my-service-token", ServiceToken: true},
		Accesses: []models.LDAccess{
			{Action: "updateRules", Resource: "proj/my-project:env/production:flag/my-flag"},
		},
	}

	acts := New(mustNewClient(t, "test-key"), newMockRepository())
	out, err := acts.NormalizeChangelog(context.Background(), NormalizeChangelogInput{
		Entry:          entry,
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
	})
	if err != nil {
		t.Fatalf("NormalizeChangelog failed: %v", err)
	}
	if out.Data.Actor.Email != "token:my-service-token" {
		t.Errorf("expected actor 'token:my-service-token', got %q", out.Data.Actor.Email)
	}
}

func TestNormalizeChangelog_NoFlagKeyInAccesses(t *testing.T) {
	acts := New(mustNewClient(t, "test-key"), newMockRepository())
	_, err := acts.NormalizeChangelog(context.Background(), NormalizeChangelogInput{
		Entry: models.LDAuditEntry{
			ID:   "audit-noflag",
			Kind: "flag",
			Date: time.Now().UnixMilli(),
			// No accesses — flag key cannot be resolved.
		},
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
	})
	if err == nil {
		t.Fatal("expected error when flag key cannot be extracted")
	}
}

func TestMapActionToEventType(t *testing.T) {
	tests := []struct {
		action   string
		expected models.EventType
	}{
		{"createFlag", models.EventTypeCreated},
		{"cloneFlag", models.EventTypeCreated},
		{"deleteFlag", models.EventTypeDeleted},
		{"updateOn", models.EventTypeToggled},
		{"updateRules", models.EventTypeUpdated},
		{"updateFallthrough", models.EventTypeUpdated},
		{"updateTargets", models.EventTypeUpdated},
		{"updateDescription", models.EventTypeUpdated},
		{"updateGlobalArchived", models.EventTypeUpdated},
		{"", models.EventTypeUpdated},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			if got := mapActionToEventType(tt.action); got != tt.expected {
				t.Errorf("mapActionToEventType(%q) = %q, want %q", tt.action, got, tt.expected)
			}
		})
	}
}

func TestResourceSegment(t *testing.T) {
	tests := []struct {
		resource string
		typ      string
		want     string
	}{
		{"proj/my-project:env/production:flag/my-flag", "flag", "my-flag"},
		{"proj/my-project:env/production:flag/my-flag", "env", "production"},
		{"proj/my-project:env/production:flag/my-flag", "proj", "my-project"},
		{"proj/my-project:env/production", "flag", ""},
		{"", "flag", ""},
	}

	for _, tt := range tests {
		t.Run(tt.resource+"/"+tt.typ, func(t *testing.T) {
			if got := resourceSegment(tt.resource, tt.typ); got != tt.want {
				t.Errorf("resourceSegment(%q, %q) = %q, want %q", tt.resource, tt.typ, got, tt.want)
			}
		})
	}
}

func TestExtractFlagKey(t *testing.T) {
	tests := []struct {
		name     string
		accesses []models.LDAccess
		want     string
	}{
		{
			name:     "standard resource string",
			accesses: []models.LDAccess{{Resource: "proj/my-project:env/production:flag/my-flag"}},
			want:     "my-flag",
		},
		{
			name: "flag key from second access",
			accesses: []models.LDAccess{
				{Resource: "proj/my-project:env/production"},
				{Resource: "proj/my-project:env/staging:flag/another-flag"},
			},
			want: "another-flag",
		},
		{
			name:     "no flag segment",
			accesses: []models.LDAccess{{Resource: "proj/my-project:env/production"}},
			want:     "",
		},
		{
			name:     "empty accesses",
			accesses: nil,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := models.LDAuditEntry{Accesses: tt.accesses}
			if got := extractFlagKey(entry); got != tt.want {
				t.Errorf("extractFlagKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractEnvironments(t *testing.T) {
	entry := models.LDAuditEntry{
		Accesses: []models.LDAccess{
			{Resource: "proj/my-project:env/production:flag/my-flag"},
			{Resource: "proj/my-project:env/staging:flag/my-flag"},
			{Resource: "proj/my-project:env/production:flag/my-flag"}, // duplicate
		},
	}

	envs := extractEnvironments(entry)
	if len(envs) != 2 {
		t.Errorf("expected 2 unique environments, got %d: %v", len(envs), envs)
	}

	has := func(s string) bool {
		for _, e := range envs {
			if e == s {
				return true
			}
		}
		return false
	}
	if !has("production") || !has("staging") {
		t.Errorf("expected production and staging, got %v", envs)
	}
}
