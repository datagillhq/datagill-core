package activities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// mockRepository implements repository.Repository for testing.
type mockRepository struct {
	sourceRecords []models.SourceRecord
	featureFlags  []models.FeatureFlag
	changelogs    []models.FeatureFlagChangelog
	integrations  map[uuid.UUID]models.Integration
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		integrations: make(map[uuid.UUID]models.Integration),
	}
}

func (r *mockRepository) SaveSourceRecord(_ context.Context, rec models.SourceRecord) error {
	r.sourceRecords = append(r.sourceRecords, rec)
	return nil
}

func (r *mockRepository) SaveFeatureFlag(_ context.Context, f models.FeatureFlag) error {
	r.featureFlags = append(r.featureFlags, f)
	return nil
}

func (r *mockRepository) SaveChangelog(_ context.Context, c models.FeatureFlagChangelog) error {
	r.changelogs = append(r.changelogs, c)
	return nil
}

func (r *mockRepository) GetIntegration(_ context.Context, key uuid.UUID) (models.Integration, error) {
	return r.integrations[key], nil
}

func (r *mockRepository) ListIntegrations(_ context.Context, _ uuid.UUID) ([]models.Integration, error) {
	var result []models.Integration
	for _, i := range r.integrations {
		result = append(result, i)
	}
	return result, nil
}

func TestListAuditLogs_Success(t *testing.T) {
	entries := []models.LDAuditEntry{
		{ID: "entry-1", Kind: "flag", Name: "test-flag", Date: time.Now().UnixMilli()},
		{ID: "entry-2", Kind: "flag", Name: "another-flag", Date: time.Now().Add(-time.Hour).UnixMilli()},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := LDAuditLogResponse{Items: entries}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-key", WithBaseURL(server.URL))
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := ListAuditLogsInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
		Before:         time.Now(),
		Limit:          20,
	}

	output, err := activities.ListAuditLogs(context.Background(), input)
	if err != nil {
		t.Fatalf("ListAuditLogs failed: %v", err)
	}

	if len(output.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(output.Entries))
	}
	if output.RecordsSaved != 2 {
		t.Errorf("expected 2 records saved, got %d", output.RecordsSaved)
	}
	if len(repo.sourceRecords) != 2 {
		t.Errorf("expected 2 source records in repo, got %d", len(repo.sourceRecords))
	}
}

func TestListAuditLogs_HasMore(t *testing.T) {
	// Return exactly limit entries to indicate there may be more
	entries := make([]models.LDAuditEntry, 20)
	for i := range entries {
		entries[i] = models.LDAuditEntry{
			ID:   "entry-" + string(rune('a'+i)),
			Kind: "flag",
			Name: "flag",
			Date: time.Now().UnixMilli(),
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := LDAuditLogResponse{Items: entries}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-key", WithBaseURL(server.URL))
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := ListAuditLogsInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
		Before:         time.Now(),
		Limit:          20,
	}

	output, err := activities.ListAuditLogs(context.Background(), input)
	if err != nil {
		t.Fatalf("ListAuditLogs failed: %v", err)
	}

	if !output.HasMore {
		t.Error("expected HasMore to be true when entries == limit")
	}
}

func TestGetFlagDetailActivity_Success(t *testing.T) {
	flag := models.LDFlagDetail{
		Key:          "my-flag",
		Name:         "My Flag",
		Kind:         "boolean",
		CreationDate: time.Now().UnixMilli(),
		Environments: map[string]models.LDFlagEnv{
			"production": {On: true},
			"staging":    {On: false},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(flag)
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-key", WithBaseURL(server.URL))
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := GetFlagDetailInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
		FlagKey:        "my-flag",
	}

	output, err := activities.GetFlagDetail(context.Background(), input)
	if err != nil {
		t.Fatalf("GetFlagDetail failed: %v", err)
	}

	if output.Flag.Key != "my-flag" {
		t.Errorf("expected flag key 'my-flag', got %q", output.Flag.Key)
	}
	if output.SourceRecordKey == uuid.Nil {
		t.Error("expected non-nil SourceRecordKey")
	}
	if len(repo.sourceRecords) != 1 {
		t.Errorf("expected 1 source record in repo, got %d", len(repo.sourceRecords))
	}
}

func TestGetFlagDetailActivity_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := mustCreateClient(t, "test-key", WithBaseURL(server.URL))
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := GetFlagDetailInput{
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
		FlagKey:        "nonexistent",
	}

	_, err := activities.GetFlagDetail(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for not found flag")
	}
}

func TestNormalizeChangelog_Created(t *testing.T) {
	entry := models.LDAuditEntry{
		ID:        "audit-123",
		Kind:      "flag",
		Name:      "new-feature",
		Date:      time.Now().UnixMilli(),
		TitleVerb: "created",
		Member: &models.LDAuditMember{
			Email: "dev@example.com",
		},
		Accesses: []models.LDAccess{
			{Resource: "proj/my-project:env/production:flag/new-feature"},
		},
	}

	client := mustCreateClient(t, "test-key")
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := NormalizeChangelogInput{
		Entry:          entry,
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
	}

	changelog, err := activities.NormalizeChangelog(context.Background(), input)
	if err != nil {
		t.Fatalf("NormalizeChangelog failed: %v", err)
	}

	if changelog.EventType != models.EventTypeCreated {
		t.Errorf("expected EventType 'created', got %q", changelog.EventType)
	}
	if changelog.Data.Actor.Email != "dev@example.com" {
		t.Errorf("expected actor email 'dev@example.com', got %q", changelog.Data.Actor.Email)
	}
	if len(changelog.Data.Environments) != 1 || changelog.Data.Environments[0] != "production" {
		t.Errorf("expected environments [production], got %v", changelog.Data.Environments)
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

	for _, tt := range tests {
		t.Run(tt.titleVerb, func(t *testing.T) {
			entry := models.LDAuditEntry{
				ID:        "audit-456",
				Kind:      "flag",
				Name:      "toggle-flag",
				Date:      time.Now().UnixMilli(),
				TitleVerb: tt.titleVerb,
			}

			client := mustCreateClient(t, "test-key")
			repo := newMockRepository()
			activities := NewLaunchDarklyActivities(client, repo)

			input := NormalizeChangelogInput{
				Entry:          entry,
				IntegrationKey: uuid.New(),
				OrgID:          uuid.New(),
				ProjectKey:     "my-project",
			}

			changelog, err := activities.NormalizeChangelog(context.Background(), input)
			if err != nil {
				t.Fatalf("NormalizeChangelog failed: %v", err)
			}

			if changelog.EventType != models.EventTypeToggled {
				t.Errorf("expected EventType 'toggled', got %q", changelog.EventType)
			}
			if changelog.State != tt.expectedState {
				t.Errorf("expected state %q, got %q", tt.expectedState, changelog.State)
			}
		})
	}
}

func TestNormalizeChangelog_NonFlagKind(t *testing.T) {
	entry := models.LDAuditEntry{
		ID:   "audit-789",
		Kind: "environment", // Not a flag
		Name: "production",
		Date: time.Now().UnixMilli(),
	}

	client := mustCreateClient(t, "test-key")
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := NormalizeChangelogInput{
		Entry:          entry,
		IntegrationKey: uuid.New(),
		OrgID:          uuid.New(),
		ProjectKey:     "my-project",
	}

	_, err := activities.NormalizeChangelog(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for non-flag kind")
	}
}

func TestNormalizeFlagDetail_AllOn(t *testing.T) {
	flag := models.LDFlagDetail{
		Key:          "feature-x",
		Name:         "Feature X",
		Description:  "A test feature",
		Kind:         "boolean",
		CreationDate: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC).UnixMilli(),
		Temporary:    true,
		Maintainer: &models.LDFlagMaintainer{
			Email: "owner@example.com",
		},
		Environments: map[string]models.LDFlagEnv{
			"production": {On: true},
			"staging":    {On: true},
		},
	}

	client := mustCreateClient(t, "test-key")
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := NormalizeFlagDetailInput{
		Flag:            flag,
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	}

	result, err := activities.NormalizeFlagDetail(context.Background(), input)
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}

	if result.State != models.FlagStateOn {
		t.Errorf("expected state 'on' when all environments are on, got %q", result.State)
	}
	if result.Data.Name != "Feature X" {
		t.Errorf("expected name 'Feature X', got %q", result.Data.Name)
	}
	if result.Data.Lifecycle.Purpose != models.LifecyclePurposeTemporary {
		t.Errorf("expected purpose 'temporary', got %q", result.Data.Lifecycle.Purpose)
	}
	if result.Data.Maintainer == nil || result.Data.Maintainer.Person == nil {
		t.Fatal("expected maintainer person to be set")
	}
	if result.Data.Maintainer.Person.Email != "owner@example.com" {
		t.Errorf("expected maintainer email 'owner@example.com', got %q", result.Data.Maintainer.Person.Email)
	}
}

func TestNormalizeFlagDetail_AllOff(t *testing.T) {
	flag := models.LDFlagDetail{
		Key:  "disabled-flag",
		Name: "Disabled Flag",
		Environments: map[string]models.LDFlagEnv{
			"production": {On: false},
			"staging":    {On: false},
		},
	}

	client := mustCreateClient(t, "test-key")
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := NormalizeFlagDetailInput{
		Flag:            flag,
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	}

	result, err := activities.NormalizeFlagDetail(context.Background(), input)
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}

	if result.State != models.FlagStateOff {
		t.Errorf("expected state 'off' when all environments are off, got %q", result.State)
	}
}

func TestNormalizeFlagDetail_Partial(t *testing.T) {
	flag := models.LDFlagDetail{
		Key:  "partial-flag",
		Name: "Partial Flag",
		Environments: map[string]models.LDFlagEnv{
			"production": {On: false},
			"staging":    {On: true},
		},
	}

	client := mustCreateClient(t, "test-key")
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := NormalizeFlagDetailInput{
		Flag:            flag,
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	}

	result, err := activities.NormalizeFlagDetail(context.Background(), input)
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}

	if result.State != models.FlagStatePartial {
		t.Errorf("expected state 'partial' when environments differ, got %q", result.State)
	}
}

func TestNormalizeFlagDetail_Archived(t *testing.T) {
	flag := models.LDFlagDetail{
		Key:      "archived-flag",
		Name:     "Archived Flag",
		Archived: true,
	}

	client := mustCreateClient(t, "test-key")
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := NormalizeFlagDetailInput{
		Flag:            flag,
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	}

	result, err := activities.NormalizeFlagDetail(context.Background(), input)
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}

	if result.Data.Lifecycle.State != models.LifecycleStateArchived {
		t.Errorf("expected lifecycle state 'archived', got %q", result.Data.Lifecycle.State)
	}
}

func TestNormalizeFlagDetail_TeamMaintainer(t *testing.T) {
	flag := models.LDFlagDetail{
		Key:  "team-flag",
		Name: "Team Flag",
		MaintainerTeam: &models.LDMaintainerTeam{
			Name: "Platform Team",
		},
	}

	client := mustCreateClient(t, "test-key")
	repo := newMockRepository()
	activities := NewLaunchDarklyActivities(client, repo)

	input := NormalizeFlagDetailInput{
		Flag:            flag,
		SourceRecordKey: uuid.New(),
		IntegrationKey:  uuid.New(),
		OrgID:           uuid.New(),
		ProjectKey:      "my-project",
	}

	result, err := activities.NormalizeFlagDetail(context.Background(), input)
	if err != nil {
		t.Fatalf("NormalizeFlagDetail failed: %v", err)
	}

	if result.Data.Maintainer == nil || result.Data.Maintainer.Team == nil {
		t.Fatal("expected maintainer team to be set")
	}
	if result.Data.Maintainer.Team.Name != "Platform Team" {
		t.Errorf("expected team name 'Platform Team', got %q", result.Data.Maintainer.Team.Name)
	}
}

func TestMapTitleVerbToEventType(t *testing.T) {
	tests := []struct {
		verb     string
		expected models.EventType
	}{
		{"created", models.EventTypeCreated},
		{"Created", models.EventTypeCreated},
		{"deleted", models.EventTypeDeleted},
		{"turned targeting on", models.EventTypeToggled},
		{"turned targeting off", models.EventTypeToggled},
		{"updated", models.EventTypeUpdated},
		{"changed", models.EventTypeUpdated},
		{"added variation", models.EventTypeUpdated},
		{"removed targeting rule", models.EventTypeUpdated},
		{"rollback", models.EventTypeRollback},
	}

	for _, tt := range tests {
		t.Run(tt.verb, func(t *testing.T) {
			result := mapTitleVerbToEventType(tt.verb)
			if result != tt.expected {
				t.Errorf("mapTitleVerbToEventType(%q) = %q, want %q", tt.verb, result, tt.expected)
			}
		})
	}
}

func TestExtractEnvironments(t *testing.T) {
	entry := models.LDAuditEntry{
		Accesses: []models.LDAccess{
			{Resource: "proj/my-project:env/production:flag/my-flag"},
			{Resource: "proj/my-project:env/staging:flag/my-flag"},
			{Resource: "proj/my-project:env/production:flag/my-flag"}, // Duplicate
		},
	}

	envs := extractEnvironments(entry)

	if len(envs) != 2 {
		t.Errorf("expected 2 unique environments, got %d: %v", len(envs), envs)
	}

	// Check both environments are present
	hasProduction := false
	hasStaging := false
	for _, e := range envs {
		if e == "production" {
			hasProduction = true
		}
		if e == "staging" {
			hasStaging = true
		}
	}
	if !hasProduction || !hasStaging {
		t.Errorf("expected production and staging, got %v", envs)
	}
}

func TestComputeFlagState(t *testing.T) {
	tests := []struct {
		name     string
		envs     map[string]models.LDFlagEnv
		expected models.FlagState
	}{
		{
			name:     "no environments",
			envs:     nil,
			expected: models.FlagStateOff,
		},
		{
			name: "all on",
			envs: map[string]models.LDFlagEnv{
				"prod": {On: true},
				"dev":  {On: true},
			},
			expected: models.FlagStateOn,
		},
		{
			name: "all off",
			envs: map[string]models.LDFlagEnv{
				"prod": {On: false},
				"dev":  {On: false},
			},
			expected: models.FlagStateOff,
		},
		{
			name: "mixed",
			envs: map[string]models.LDFlagEnv{
				"prod": {On: true},
				"dev":  {On: false},
			},
			expected: models.FlagStatePartial,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := models.LDFlagDetail{Environments: tt.envs}
			result := computeFlagState(flag)
			if result != tt.expected {
				t.Errorf("computeFlagState() = %q, want %q", result, tt.expected)
			}
		})
	}
}
