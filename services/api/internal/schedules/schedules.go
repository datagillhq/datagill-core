// Package schedules creates the Temporal schedules that drive each integration's sync.
package schedules

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

// Workflow names, task queue and inputs mirror what cmd/worker registers. They are
// duplicated rather than imported so this service doesn't depend on the workflows module;
// the tests pin the literal values so a rename on either side fails loudly.
const (
	taskQueue = "etl"

	etlWorkflow = "FeatureFlagETLWorkflow"
	etlInterval = 30 * time.Minute
	etlJitter   = 30 * time.Second

	enrichmentWorkflow = "GitHubEnrichmentWorkflow"
	enrichmentInterval = 30 * time.Minute
)

// etlInput mirrors workflows.ETLWorkflowInput.
type etlInput struct {
	OrgID          uuid.UUID
	IntegrationKey uuid.UUID
}

// enrichmentInput mirrors workflows/github.EnrichmentInput.
type enrichmentInput struct {
	OrgID uuid.UUID
}

// Dial connects to the Temporal frontend at address.
func Dial(address string) (client.Client, error) {
	c, err := client.Dial(client.Options{HostPort: address})
	if err != nil {
		return nil, fmt.Errorf("dial temporal at %s: %w", address, err)
	}
	return c, nil
}

// Temporal creates schedules through a Temporal client.
type Temporal struct {
	client client.Client
}

// NewTemporal returns a Temporal scheduler using c.
func NewTemporal(c client.Client) *Temporal {
	return &Temporal{client: c}
}

// Create ensures every schedule the integration needs exists: one ETL schedule per
// integration and, for GitHub, one PR enrichment schedule per org. Schedules that already
// exist are left as they are, so a retried connect fills in whatever is missing.
func (t *Temporal) Create(ctx context.Context, integration models.Integration) error {
	err := t.ensure(ctx, schedule{
		id:       ETLScheduleID(integration.Key),
		every:    etlInterval,
		offset:   etlJitter,
		workflow: etlWorkflow,
		input:    etlInput{OrgID: integration.OrgID, IntegrationKey: integration.Key},
	})
	if err != nil {
		return err
	}

	if integration.Source != models.SourceGitHub {
		return nil
	}
	return t.ensure(ctx, schedule{
		id:       EnrichmentScheduleID(integration.OrgID),
		every:    enrichmentInterval,
		offset:   enrichmentOffset(integration.OrgID),
		workflow: enrichmentWorkflow,
		input:    enrichmentInput{OrgID: integration.OrgID},
	})
}

type schedule struct {
	id       string
	every    time.Duration
	offset   time.Duration
	workflow string
	input    any
}

func (t *Temporal) ensure(ctx context.Context, s schedule) error {
	_, err := t.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: s.id,
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{{Every: s.every, Offset: s.offset}},
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        s.id, // reused as the workflow ID; each run gets its own run ID
			Workflow:  s.workflow,
			Args:      []any{s.input},
			TaskQueue: taskQueue,
		},
		Overlap:            enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		TriggerImmediately: true, // start the first sync now rather than at the next tick
	})
	// The SDK reports an existing schedule ID as ErrScheduleAlreadyRunning.
	if err != nil && !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return fmt.Errorf("create schedule %s: %w", s.id, err)
	}
	return nil
}

// ETLScheduleID is the deterministic schedule ID for an integration's ETL.
func ETLScheduleID(integrationKey uuid.UUID) string {
	return "etl-" + integrationKey.String()
}

// EnrichmentScheduleID is the deterministic schedule ID for an org's PR enrichment.
// Existing schedules are found by this ID, so changing the format orphans them.
func EnrichmentScheduleID(orgID uuid.UUID) string {
	return "github-pr-enrichment-" + orgID.String()
}

// enrichmentOffset spreads orgs across the enrichment interval. Temporal interval
// schedules fire at fixed clock times (epoch + n*every + offset), so without an offset
// every org would run at :00 and :30.
func enrichmentOffset(orgID uuid.UUID) time.Duration {
	h := fnv.New64a()
	_, _ = h.Write(orgID[:])
	seconds := uint64(enrichmentInterval / time.Second)
	return time.Duration(h.Sum64()%seconds) * time.Second
}
