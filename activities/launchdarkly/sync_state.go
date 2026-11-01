package launchdarkly

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// UpdateLastSyncedAtInput is the input for the UpdateLastSyncedAt activity.
type UpdateLastSyncedAtInput struct {
	IntegrationKey uuid.UUID
	SyncedAt       time.Time
}

// UpdateLastSyncedAt records the high-water mark for incremental syncs.
// Safe to retry — always overwrites with the provided timestamp.
func (a *Activities) UpdateLastSyncedAt(ctx context.Context, input UpdateLastSyncedAtInput) error {
	if err := a.repo.UpdateIntegrationLastSyncedAt(ctx, input.IntegrationKey, input.SyncedAt); err != nil {
		return fmt.Errorf("UpdateLastSyncedAt: %w", err)
	}
	return nil
}
