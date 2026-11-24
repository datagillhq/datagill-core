package repository

import (
	"context"
	"fmt"

	"github.com/datagill/datagill-core/models"
)

// SaveSourceRecord persists a raw provider API response.
// Uses ON CONFLICT DO NOTHING since source_records is append-only.
func (r *PostgresRepository) SaveSourceRecord(ctx context.Context, rec models.SourceRecord) error {
	query := `
		INSERT INTO source_records (key, integration_id, org_id, source_type, data, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key) DO NOTHING
	`

	_, err := r.pool.Exec(ctx, query,
		rec.Key,
		rec.IntegrationID,
		rec.OrgID,
		rec.SourceType,
		rec.Data,
		orNow(rec.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("SaveSourceRecord: %w", err)
	}

	return nil
}
