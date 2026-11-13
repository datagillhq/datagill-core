package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/datagill/datagill-core/models"
)

// SaveChangelog upserts a normalized changelog entry.
func (r *PostgresRepository) SaveChangelog(ctx context.Context, c models.FeatureFlagChangelog) error {
	dataJSON, err := json.Marshal(c.Data)
	if err != nil {
		return fmt.Errorf("SaveChangelog marshal data: %w", err)
	}

	query := `
		INSERT INTO feature_flag_changelog (key, source_record_key, feature_flag_key, integration_id, org_id, event_type, state, occurred_at, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (key) DO UPDATE SET
			state = EXCLUDED.state,
			data = EXCLUDED.data,
			updated_at = EXCLUDED.updated_at
	`

	_, err = r.pool.Exec(ctx, query,
		c.Key,
		c.SourceRecordKey,
		c.FeatureFlagKey,
		c.IntegrationID,
		c.OrgID,
		c.EventType,
		c.State,
		c.OccurredAt,
		dataJSON,
		orNow(c.CreatedAt),
		orNow(c.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("SaveChangelog: %w", err)
	}

	return nil
}
