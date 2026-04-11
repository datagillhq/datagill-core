package launchdarkly

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUpdateLastSyncedAt_Success(t *testing.T) {
	repo := newMockRepository()
	acts := New(mustNewClient(t, "test-key"), repo)

	err := acts.UpdateLastSyncedAt(context.Background(), UpdateLastSyncedAtInput{
		IntegrationKey: uuid.New(),
		SyncedAt:       time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateLastSyncedAt failed: %v", err)
	}
}
