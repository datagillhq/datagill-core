package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

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

// GetFeatureFlagsBySourceKeys returns all non-deleted flags for an org whose
// provider key matches any of the given source keys.
func (r *PostgresRepository) GetFeatureFlagsBySourceKeys(ctx context.Context, orgID uuid.UUID, sourceKeys []string) ([]models.FeatureFlag, error) {
	if len(sourceKeys) == 0 {
		return nil, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT key, source_record_key, integration_id, org_id, state, data, created_at, updated_at
		FROM feature_flags
		WHERE org_id = $1
		  AND data->'source'->>'key' = ANY($2::text[])
		  AND state != 'deleted'
	`, orgID, sourceKeys)
	if err != nil {
		return nil, fmt.Errorf("GetFeatureFlagsBySourceKeys: %w", err)
	}
	defer rows.Close()

	var flags []models.FeatureFlag
	for rows.Next() {
		var f models.FeatureFlag
		var dataJSON []byte
		if err := rows.Scan(
			&f.Key, &f.SourceRecordKey, &f.IntegrationID, &f.OrgID,
			&f.State, &dataJSON, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetFeatureFlagsBySourceKeys scan: %w", err)
		}
		if err := json.Unmarshal(dataJSON, &f.Data); err != nil {
			return nil, fmt.Errorf("GetFeatureFlagsBySourceKeys unmarshal: %w", err)
		}
		flags = append(flags, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetFeatureFlagsBySourceKeys rows: %w", err)
	}
	return flags, nil
}

// ListFeatureFlagSourceKeys returns all provider key strings for non-deleted flags in an org.
func (r *PostgresRepository) ListFeatureFlagSourceKeys(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT data->'source'->>'key'
		FROM feature_flags
		WHERE org_id = $1
		  AND state != 'deleted'
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("ListFeatureFlagSourceKeys: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("ListFeatureFlagSourceKeys scan: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListFeatureFlagSourceKeys rows: %w", err)
	}
	return keys, nil
}
