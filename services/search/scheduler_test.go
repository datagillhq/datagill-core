package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	enumspb "go.temporal.io/api/enums/v1"
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

	var got client.ScheduleOptions
	scheduleClient.On("Create", mock.Anything, isETLSchedule(integration.Key)).Return(nil, nil).Once()
	scheduleClient.On("Create", mock.Anything, isEnrichmentSchedule(integration.OrgID)).
		Run(func(args mock.Arguments) { got = args.Get(1).(client.ScheduleOptions) }).
		Return(nil, nil).Once()

	if err := scheduler.Create(context.Background(), integration); err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertEnrichmentScheduleOptions(t, got, integration.OrgID)
}

// assertEnrichmentScheduleOptions pins values hand-mirrored from workflows/github, which this
// module can't import. Literals are used on purpose so a rename on either side fails here.
func assertEnrichmentScheduleOptions(t *testing.T, got client.ScheduleOptions, orgID uuid.UUID) {
	t.Helper()

	action, ok := got.Action.(*client.ScheduleWorkflowAction)
	if !ok {
		t.Fatalf("expected *client.ScheduleWorkflowAction, got %T", got.Action)
	}
	if action.Workflow != "GitHubEnrichmentWorkflow" {
		t.Errorf("workflow = %v, want GitHubEnrichmentWorkflow (must match the name registered in cmd/worker)", action.Workflow)
	}
	if action.TaskQueue != "etl" {
		t.Errorf("task queue = %q, want etl", action.TaskQueue)
	}
	if action.ID != enrichmentScheduleIDFor(orgID) {
		t.Errorf("workflow ID = %q, want %q", action.ID, enrichmentScheduleIDFor(orgID))
	}
	if len(action.Args) != 1 {
		t.Fatalf("expected 1 workflow arg, got %d", len(action.Args))
	}
	// The worker decodes this into workflows/github.EnrichmentInput{OrgID uuid.UUID}.
	encoded, err := json.Marshal(action.Args[0])
	if err != nil {
		t.Fatalf("marshal workflow arg: %v", err)
	}
	if want := `{"OrgID":"` + orgID.String() + `"}`; string(encoded) != want {
		t.Errorf("workflow arg JSON = %s, want %s", encoded, want)
	}

	if got.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_SKIP {
		t.Errorf("overlap = %v, want SKIP", got.Overlap)
	}
	if !got.TriggerImmediately {
		t.Error("expected TriggerImmediately")
	}
	wantInterval := client.ScheduleIntervalSpec{Every: 30 * time.Minute, Offset: enrichmentOffsetFor(orgID)}
	if len(got.Spec.Intervals) != 1 || got.Spec.Intervals[0] != wantInterval {
		t.Errorf("intervals = %+v, want [%+v]", got.Spec.Intervals, wantInterval)
	}
}

// A retried connect finds the schedules from the earlier attempt; both must be accepted
// so the retry can fill in whatever is missing.
func TestTemporalScheduler_CreateGitHubToleratesExistingSchedules(t *testing.T) {
	scheduler, scheduleClient := newMockScheduler(t)
	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub}

	scheduleClient.On("Create", mock.Anything, isETLSchedule(integration.Key)).
		Return(nil, temporal.ErrScheduleAlreadyRunning).Once()
	scheduleClient.On("Create", mock.Anything, isEnrichmentSchedule(integration.OrgID)).
		Return(nil, temporal.ErrScheduleAlreadyRunning).Once()

	if err := scheduler.Create(context.Background(), integration); err != nil {
		t.Fatalf("expected existing schedules to be tolerated, got %v", err)
	}
}

func TestTemporalScheduler_CreateReturnsOtherETLErrors(t *testing.T) {
	scheduler, scheduleClient := newMockScheduler(t)
	integration := models.Integration{Key: uuid.New(), OrgID: uuid.New(), Source: models.SourceGitHub}
	temporalDown := errors.New("temporal unavailable")

	scheduleClient.On("Create", mock.Anything, isETLSchedule(integration.Key)).Return(nil, temporalDown).Once()

	if err := scheduler.Create(context.Background(), integration); !errors.Is(err, temporalDown) {
		t.Fatalf("expected etl schedule error to be returned, got %v", err)
	}
}

func TestEnrichmentOffsetFor(t *testing.T) {
	orgID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if a, b := enrichmentOffsetFor(orgID), enrichmentOffsetFor(orgID); a != b {
		t.Fatalf("offset not stable for the same org: %v vs %v", a, b)
	}

	// Offsets must stay inside the interval and spread orgs across it.
	seen := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		offset := enrichmentOffsetFor(uuid.New())
		if offset < 0 || offset >= 30*time.Minute {
			t.Fatalf("offset %v outside [0, 30m)", offset)
		}
		if offset%time.Second != 0 {
			t.Fatalf("offset %v is not whole seconds", offset)
		}
		seen[offset/time.Minute] = true
	}
	if len(seen) < 20 {
		t.Fatalf("offsets not spread: only %d distinct minutes out of 30 across 200 orgs", len(seen))
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
