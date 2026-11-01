package main

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/datagill/datagill-core/models"
)

const (
	etlTaskQueue    = "etl"
	etlWorkflowType = "FeatureFlagETLWorkflow"
	etlInterval     = 30 * time.Minute
	etlJitter       = 30 * time.Second

	enrichmentWorkflowType = "GitHubEnrichmentWorkflow"
	enrichmentInterval     = 30 * time.Minute
)

// etlWorkflowInput mirrors workflows.ETLWorkflowInput for JSON serialization.
// Defined locally to avoid importing the workflows module into the search service.
type etlWorkflowInput struct {
	OrgID          uuid.UUID
	IntegrationKey uuid.UUID
}

// enrichmentWorkflowInput mirrors workflows/github.EnrichmentInput.
type enrichmentWorkflowInput struct {
	OrgID uuid.UUID
}

// Scheduler manages Temporal schedules for ETL integrations.
type Scheduler interface {
	// Create ensures every schedule the integration needs exists. It is idempotent:
	// schedules that already exist are left as they are, so callers can retry after a
	// partial failure.
	Create(ctx context.Context, integration models.Integration) error
}

// temporalScheduler implements Scheduler using the Temporal schedule client.
type temporalScheduler struct {
	client client.Client
}

func newTemporalClient(address string) (client.Client, error) {
	c, err := client.Dial(client.Options{HostPort: address})
	if err != nil {
		return nil, fmt.Errorf("dial temporal at %s: %w", address, err)
	}
	return c, nil
}

func newTemporalScheduler(tc client.Client) Scheduler {
	return &temporalScheduler{client: tc}
}

func (s *temporalScheduler) Create(ctx context.Context, integration models.Integration) error {
	if err := s.createETLSchedule(ctx, integration); err != nil {
		return err
	}
	if integration.Source == models.SourceGitHub {
		if err := s.ensureEnrichmentSchedule(ctx, integration.OrgID); err != nil {
			return err
		}
	}
	return nil
}

func (s *temporalScheduler) createETLSchedule(ctx context.Context, integration models.Integration) error {
	scheduleID := scheduleIDFor(integration.Key)
	_, err := s.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: scheduleID,
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{
				{Every: etlInterval, Offset: etlJitter},
			},
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        scheduleID, // reuse as workflow ID — each run gets a unique run ID
			Workflow:  etlWorkflowType,
			Args:      []any{etlWorkflowInput{OrgID: integration.OrgID, IntegrationKey: integration.Key}},
			TaskQueue: etlTaskQueue,
		},
		Overlap:            enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		TriggerImmediately: true, // kick off the first sync right away
	})
	// The SDK maps Temporal's duplicate-schedule response to ErrScheduleAlreadyRunning;
	// a retried connect finds the schedule from the earlier attempt.
	if err != nil && !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return fmt.Errorf("create etl schedule %s: %w", scheduleID, err)
	}
	return nil
}

// ensureEnrichmentSchedule creates a per-org PR enrichment schedule if one does not exist.
func (s *temporalScheduler) ensureEnrichmentSchedule(ctx context.Context, orgID uuid.UUID) error {
	scheduleID := enrichmentScheduleIDFor(orgID)
	_, err := s.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: scheduleID,
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{
				{Every: enrichmentInterval, Offset: enrichmentOffsetFor(orgID)},
			},
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        scheduleID,
			Workflow:  enrichmentWorkflowType,
			Args:      []any{enrichmentWorkflowInput{OrgID: orgID}},
			TaskQueue: etlTaskQueue,
		},
		Overlap:            enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		TriggerImmediately: true,
	})
	// A second GitHub integration in the same org, or a retried connect, reuses the
	// existing schedule.
	if err != nil && !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return fmt.Errorf("create enrichment schedule %s: %w", scheduleID, err)
	}
	return nil
}

// scheduleIDFor returns the deterministic Temporal schedule ID for an integration.
func scheduleIDFor(integrationKey uuid.UUID) string {
	return "etl-" + integrationKey.String()
}

// enrichmentScheduleIDFor returns the deterministic enrichment schedule ID for an org.
func enrichmentScheduleIDFor(orgID uuid.UUID) string {
	return "github-pr-enrichment-" + orgID.String()
}

// enrichmentOffsetFor returns a stable per-org offset within the enrichment interval.
// Temporal interval schedules fire at fixed clock times (epoch + n*every + offset), not
// relative to the last run, so without an offset every org would run at :00 and :30.
func enrichmentOffsetFor(orgID uuid.UUID) time.Duration {
	h := fnv.New64a()
	_, _ = h.Write(orgID[:])
	seconds := uint64(enrichmentInterval / time.Second)
	return time.Duration(h.Sum64()%seconds) * time.Second
}
