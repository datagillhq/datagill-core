package launchdarkly

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

func TestNormalizeFlagDetail_AllOn(t *testing.T) {
	acts := New(mustNewClient(t, "test-key"), newMockRepository())
	out, err := acts.NormalizeFlagDetail(context.Background(), NormalizeFlagDetailInput{
		Flag: models.LDFlagDetail{
			Key:          "feature-x",
			Name:         "Feature X",
			Description:  "A test feature",
			Kind:         "boolean",
			CreationDate: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC).UnixMilli(),
			Temporary:    true,
			Maintainer:   &models.LDFlagMaintainer{Email: "owner@example.com"},
			Environments: map[string]models.LDFlagEnv{
				"production": {On: true},
				"staging":    {On: true},
			},
		},
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	})
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}
	if out.State != models.FlagStateOn {
		t.Errorf("expected state 'on', got %q", out.State)
	}
	if out.Data.Name != "Feature X" {
		t.Errorf("expected name 'Feature X', got %q", out.Data.Name)
	}
	if out.Data.Lifecycle.Purpose != models.LifecyclePurposeTemporary {
		t.Errorf("expected purpose 'temporary', got %q", out.Data.Lifecycle.Purpose)
	}
	if out.Data.Maintainer == nil || out.Data.Maintainer.Person == nil {
		t.Fatal("expected maintainer person to be set")
	}
	if out.Data.Maintainer.Person.Email != "owner@example.com" {
		t.Errorf("expected maintainer email 'owner@example.com', got %q", out.Data.Maintainer.Person.Email)
	}
}

func TestNormalizeFlagDetail_AllOff(t *testing.T) {
	acts := New(mustNewClient(t, "test-key"), newMockRepository())
	out, err := acts.NormalizeFlagDetail(context.Background(), NormalizeFlagDetailInput{
		Flag: models.LDFlagDetail{
			Key:  "disabled-flag",
			Name: "Disabled Flag",
			Environments: map[string]models.LDFlagEnv{
				"production": {On: false},
				"staging":    {On: false},
			},
		},
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	})
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}
	if out.State != models.FlagStateOff {
		t.Errorf("expected state 'off', got %q", out.State)
	}
}

func TestNormalizeFlagDetail_Partial(t *testing.T) {
	acts := New(mustNewClient(t, "test-key"), newMockRepository())
	out, err := acts.NormalizeFlagDetail(context.Background(), NormalizeFlagDetailInput{
		Flag: models.LDFlagDetail{
			Key:  "partial-flag",
			Name: "Partial Flag",
			Environments: map[string]models.LDFlagEnv{
				"production": {On: false},
				"staging":    {On: true},
			},
		},
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	})
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}
	if out.State != models.FlagStatePartial {
		t.Errorf("expected state 'partial', got %q", out.State)
	}
}

func TestNormalizeFlagDetail_Archived(t *testing.T) {
	acts := New(mustNewClient(t, "test-key"), newMockRepository())
	out, err := acts.NormalizeFlagDetail(context.Background(), NormalizeFlagDetailInput{
		Flag:            models.LDFlagDetail{Key: "archived-flag", Name: "Archived Flag", Archived: true},
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	})
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}
	if out.Data.Lifecycle.State != models.LifecycleStateArchived {
		t.Errorf("expected lifecycle state 'archived', got %q", out.Data.Lifecycle.State)
	}
}

func TestNormalizeFlagDetail_TeamMaintainer(t *testing.T) {
	acts := New(mustNewClient(t, "test-key"), newMockRepository())
	out, err := acts.NormalizeFlagDetail(context.Background(), NormalizeFlagDetailInput{
		Flag: models.LDFlagDetail{
			Key:            "team-flag",
			Name:           "Team Flag",
			MaintainerTeam: &models.LDMaintainerTeam{Name: "Platform Team"},
		},
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	})
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}
	if out.Data.Maintainer == nil || out.Data.Maintainer.Team == nil {
		t.Fatal("expected maintainer team to be set")
	}
	if out.Data.Maintainer.Team.Name != "Platform Team" {
		t.Errorf("expected team name 'Platform Team', got %q", out.Data.Maintainer.Team.Name)
	}
}

func TestComputeFlagState(t *testing.T) {
	tests := []struct {
		name     string
		envs     map[string]models.LDFlagEnv
		expected models.FlagState
	}{
		{"no environments", nil, models.FlagStateOff},
		{"all on", map[string]models.LDFlagEnv{"prod": {On: true}, "dev": {On: true}}, models.FlagStateOn},
		{"all off", map[string]models.LDFlagEnv{"prod": {On: false}, "dev": {On: false}}, models.FlagStateOff},
		{"mixed", map[string]models.LDFlagEnv{"prod": {On: true}, "dev": {On: false}}, models.FlagStatePartial},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := computeFlagState(models.LDFlagDetail{Environments: tt.envs}); got != tt.expected {
				t.Errorf("computeFlagState() = %q, want %q", got, tt.expected)
			}
		})
	}
}
