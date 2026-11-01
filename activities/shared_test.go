package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
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

func TestSharedActivities_GetIntegration_Success(t *testing.T) {
	repo := newMockRepository()
	orgID := uuid.New()
	key := uuid.New()
	repo.integrations[key] = models.Integration{Key: key, OrgID: orgID, Source: models.SourceLaunchDarkly}
	acts := NewSharedActivities(repo)

	integration, err := acts.GetIntegration(context.Background(), orgID, key)
	if err != nil {
		t.Fatalf("GetIntegration failed: %v", err)
	}
	if integration.Key != key {
		t.Errorf("expected key %s, got %s", key, integration.Key)
	}
}

// getIntegrationErrRepository returns a fixed error from GetIntegration.
type getIntegrationErrRepository struct {
	mockRepository
	err error
}

func (r *getIntegrationErrRepository) GetIntegration(_ context.Context, _, _ uuid.UUID) (models.Integration, error) {
	return models.Integration{}, r.err
}

// A deleted integration must stop the workflow instead of being retried forever.
func TestSharedActivities_GetIntegration_NotFoundIsNonRetryable(t *testing.T) {
	acts := NewSharedActivities(&getIntegrationErrRepository{mockRepository: *newMockRepository(), err: repository.ErrNotFound})

	_, err := acts.GetIntegration(context.Background(), uuid.New(), uuid.New())

	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected ApplicationError, got %T: %v", err, err)
	}
	if !appErr.NonRetryable() {
		t.Error("expected not-found error to be non-retryable")
	}
	if !IsIntegrationNotFound(err) {
		t.Errorf("expected IsIntegrationNotFound to match, got type %q", appErr.Type())
	}
}

func TestSharedActivities_GetIntegration_OtherErrorsStayRetryable(t *testing.T) {
	dbDown := errors.New("connection refused")
	acts := NewSharedActivities(&getIntegrationErrRepository{mockRepository: *newMockRepository(), err: dbDown})

	_, err := acts.GetIntegration(context.Background(), uuid.New(), uuid.New())

	if !errors.Is(err, dbDown) {
		t.Fatalf("expected wrapped db error, got %v", err)
	}
	if IsIntegrationNotFound(err) {
		t.Error("a transient error must not be treated as not found")
	}
}
