package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/temporal"

	"github.com/datagill/datagill-core/models"
)

func isEnrichmentSchedule(orgID uuid.UUID) any {
	return mock.MatchedBy(func(options client.ScheduleOptions) bool {
		return options.ID == enrichmentScheduleIDFor(orgID)
	})
}

func isETLSchedule(integrationKey uuid.UUID) any {
	return mock.MatchedBy(func(options client.ScheduleOptions) bool {
		return options.ID == scheduleIDFor(integrationKey)
	})
}

func newMockScheduler(t *testing.T) (*temporalScheduler, *mocks.ScheduleClient) {
	t.Helper()
	scheduleClient := mocks.NewScheduleClient(t)
	temporalClient := mocks.NewClient(t)
	temporalClient.On("ScheduleClient").Return(scheduleClient)
	return &temporalScheduler{client: temporalClient}, scheduleClient
}

func TestTemporalScheduler_CreateGitHubCreatesETLAndEnrichmentSchedules(t *testing.T) {
	scheduler, scheduleClient := newMockScheduler(t)
	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub}

	scheduleClient.On("Create", mock.Anything, isETLSchedule(integration.Key)).Return(nil, nil).Once()
	scheduleClient.On("Create", mock.Anything, isEnrichmentSchedule(integration.OrgID)).Return(nil, nil).Once()

	if err := scheduler.Create(context.Background(), integration); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// A second GitHub integration in the same org must reuse the org's existing enrichment
// schedule instead of failing the connect flow.
func TestTemporalScheduler_CreateGitHubToleratesExistingEnrichmentSchedule(t *testing.T) {
	scheduler, scheduleClient := newMockScheduler(t)
	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub}

	scheduleClient.On("Create", mock.Anything, isETLSchedule(integration.Key)).Return(nil, nil).Once()
	scheduleClient.On("Create", mock.Anything, isEnrichmentSchedule(integration.OrgID)).
		Return(nil, temporal.ErrScheduleAlreadyRunning).Once()

	if err := scheduler.Create(context.Background(), integration); err != nil {
		t.Fatalf("expected existing enrichment schedule to be tolerated, got %v", err)
	}
}

func TestTemporalScheduler_CreateGitHubReturnsOtherEnrichmentErrors(t *testing.T) {
	scheduler, scheduleClient := newMockScheduler(t)
	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub}
	temporalDown := errors.New("temporal unavailable")

	scheduleClient.On("Create", mock.Anything, isETLSchedule(integration.Key)).Return(nil, nil).Once()
	scheduleClient.On("Create", mock.Anything, isEnrichmentSchedule(integration.OrgID)).Return(nil, temporalDown).Once()

	if err := scheduler.Create(context.Background(), integration); !errors.Is(err, temporalDown) {
		t.Fatalf("expected enrichment schedule error to be returned, got %v", err)
	}
}

// Existing orgs' schedules are looked up by this ID, so changing the format would orphan
// them and create a duplicate schedule on the next GitHub connect.
func TestEnrichmentScheduleIDFor(t *testing.T) {
	tests := []struct {
		name  string
		orgID uuid.UUID
		want  string
	}{
		{
			name:  "typical org",
			orgID: uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8"),
			want:  "github-pr-enrichment-6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		},
		{
			name:  "another org gets its own ID",
			orgID: uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
			want:  "github-pr-enrichment-f47ac10b-58cc-4372-a567-0e02b2c3d479",
		},
		{
			name:  "nil org",
			orgID: uuid.Nil,
			want:  "github-pr-enrichment-00000000-0000-0000-0000-000000000000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := enrichmentScheduleIDFor(tt.orgID); got != tt.want {
				t.Errorf("enrichmentScheduleIDFor(%s) = %q, want %q", tt.orgID, got, tt.want)
			}
		})
	}
}

func TestTemporalScheduler_CreateLaunchDarklySkipsEnrichmentSchedule(t *testing.T) {
	scheduler, scheduleClient := newMockScheduler(t)
	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceLaunchDarkly}

	scheduleClient.On("Create", mock.Anything, isETLSchedule(integration.Key)).Return(nil, nil).Once()

	if err := scheduler.Create(context.Background(), integration); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
