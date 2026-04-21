package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/datagill/datagill-core/models"
)

// SaveFeatureFlag upserts the normalized state of a feature flag.
func (r *PostgresRepository) SaveFeatureFlag(ctx context.Context, f models.FeatureFlag) error {
	dataJSON, err := json.Marshal(f.Data)
	if err != nil {
		return fmt.Errorf("SaveFeatureFlag marshal data: %w", err)
	}

	query := `
		INSERT INTO feature_flags (key, source_record_key, integration_id, org_id, state, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (key) DO UPDATE SET
			source_record_key = EXCLUDED.source_record_key,
			state = EXCLUDED.state,
			data = EXCLUDED.data,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at
	`

	_, err = r.pool.Exec(ctx, query,
		f.Key,
		f.SourceRecordKey,
		f.IntegrationID,
		f.OrgID,
		f.State,
		dataJSON,
		f.CreatedAt,
		f.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("SaveFeatureFlag: %w", err)
	}

	return nil
}
