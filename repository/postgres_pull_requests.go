package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/datagill/datagill-core/models"
)

// SavePullRequest upserts the normalized state of a pull request.
func (r *PostgresRepository) SavePullRequest(ctx context.Context, pr models.PullRequest) error {
	dataJSON, err := json.Marshal(pr.Data)
	if err != nil {
		return fmt.Errorf("SavePullRequest marshal data: %w", err)
	}

	query := `
		INSERT INTO pull_requests (key, source_record_key, integration_id, org_id, state, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (key) DO UPDATE SET
			source_record_key = EXCLUDED.source_record_key,
			state = EXCLUDED.state,
			data = EXCLUDED.data,
			updated_at = EXCLUDED.updated_at
	`

	_, err = r.pool.Exec(ctx, query,
		pr.Key,
		pr.SourceRecordKey,
		pr.IntegrationID,
		pr.OrgID,
		pr.State,
		dataJSON,
		pr.CreatedAt,
		pr.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("SavePullRequest: %w", err)
	}

	return nil
}
