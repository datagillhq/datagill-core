package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// errorRepository returns errors on save operations for testing error handling.
type errorRepository struct {
	mockRepository
	saveSourceRecordErr error
	saveFeatureFlagErr  error
	saveChangelogErr    error
}

func (r *errorRepository) SaveSourceRecord(_ context.Context, _ models.SourceRecord) error {
	return r.saveSourceRecordErr
}

func (r *errorRepository) SaveFeatureFlag(_ context.Context, _ models.FeatureFlag) error {
	return r.saveFeatureFlagErr
}

func (r *errorRepository) SaveChangelog(_ context.Context, _ models.FeatureFlagChangelog) error {
	return r.saveChangelogErr
}

func TestSharedActivities_SaveSourceRecord_Success(t *testing.T) {
	repo := newMockRepository()
	activities := NewSharedActivities(repo)

	record := models.SourceRecord{
		Key:           uuid.New(),
		IntegrationID: uuid.New(),
		OrgID:         uuid.New(),
		SourceType:    models.SourceTypeAuditLog,
		Data:          []byte(`{"test": "data"}`),
	}

	err := activities.SaveSourceRecord(context.Background(), record)
	if err != nil {
		t.Fatalf("SaveSourceRecord failed: %v", err)
	}

	if len(repo.sourceRecords) != 1 {
		t.Errorf("expected 1 source record, got %d", len(repo.sourceRecords))
	}
}

func TestSharedActivities_SaveSourceRecord_Error(t *testing.T) {
	repo := &errorRepository{
		saveSourceRecordErr: errors.New("database connection failed"),
	}
	activities := NewSharedActivities(repo)

	record := models.SourceRecord{
		Key: uuid.New(),
	}

	err := activities.SaveSourceRecord(context.Background(), record)
	if err == nil {
		t.Fatal("expected error when repository fails")
	}
	if !errors.Is(err, repo.saveSourceRecordErr) {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestSharedActivities_SaveFeatureFlag_Success(t *testing.T) {
	repo := newMockRepository()
	activities := NewSharedActivities(repo)

	flag := models.FeatureFlag{
		Key:           uuid.New(),
		IntegrationID: uuid.New(),
		OrgID:         uuid.New(),
		State:         models.FlagStateOn,
	}

	err := activities.SaveFeatureFlag(context.Background(), flag)
	if err != nil {
		t.Fatalf("SaveFeatureFlag failed: %v", err)
	}

	if len(repo.featureFlags) != 1 {
		t.Errorf("expected 1 feature flag, got %d", len(repo.featureFlags))
	}
}

func TestSharedActivities_SaveFeatureFlag_Error(t *testing.T) {
	repo := &errorRepository{
		saveFeatureFlagErr: errors.New("constraint violation"),
	}
	activities := NewSharedActivities(repo)

	flag := models.FeatureFlag{
		Key: uuid.New(),
	}

	err := activities.SaveFeatureFlag(context.Background(), flag)
	if err == nil {
		t.Fatal("expected error when repository fails")
	}
}

func TestSharedActivities_SaveChangelog_Success(t *testing.T) {
	repo := newMockRepository()
	activities := NewSharedActivities(repo)

	changelog := models.FeatureFlagChangelog{
		Key:           uuid.New(),
		IntegrationID: uuid.New(),
		OrgID:         uuid.New(),
		EventType:     models.EventTypeCreated,
	}

	err := activities.SaveChangelog(context.Background(), changelog)
	if err != nil {
		t.Fatalf("SaveChangelog failed: %v", err)
	}

	if len(repo.changelogs) != 1 {
		t.Errorf("expected 1 changelog, got %d", len(repo.changelogs))
	}
}

func TestSharedActivities_SaveChangelog_Error(t *testing.T) {
	repo := &errorRepository{
		saveChangelogErr: errors.New("timeout"),
	}
	activities := NewSharedActivities(repo)

	changelog := models.FeatureFlagChangelog{
		Key: uuid.New(),
	}

	err := activities.SaveChangelog(context.Background(), changelog)
	if err == nil {
		t.Fatal("expected error when repository fails")
	}
}
