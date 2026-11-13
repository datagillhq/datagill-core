package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// GetPullRequestByKey returns a single pull request by its key, scoped to the org.
func (r *PostgresRepository) GetPullRequestByKey(ctx context.Context, orgID, key uuid.UUID) (models.PullRequest, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT key, source_record_key, integration_id, org_id, state, data, created_at, updated_at
		FROM pull_requests
		WHERE org_id = $1 AND key = $2
	`, orgID, key)

	var pr models.PullRequest
	var dataJSON []byte
	err := row.Scan(
		&pr.Key,
		&pr.SourceRecordKey,
		&pr.IntegrationID,
		&pr.OrgID,
		&pr.State,
		&dataJSON,
		&pr.CreatedAt,
		&pr.UpdatedAt,
	)
	if err != nil {
		return models.PullRequest{}, fmt.Errorf("GetPullRequestByKey: %w", err)
	}
	if err := json.Unmarshal(dataJSON, &pr.Data); err != nil {
		return models.PullRequest{}, fmt.Errorf("GetPullRequestByKey unmarshal: %w", err)
	}
	return pr, nil
}

// SavePullRequest upserts the normalized state of a pull request and ensures a
// corresponding pull_request_enrichments row exists. Both writes are atomic.
// On re-ingest of an existing PR the enrichment row is left untouched.
func (r *PostgresRepository) SavePullRequest(ctx context.Context, pr models.PullRequest) error {
	dataJSON, err := json.Marshal(pr.Data)
	if err != nil {
		return fmt.Errorf("SavePullRequest marshal data: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("SavePullRequest begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO pull_requests (key, source_record_key, integration_id, org_id, state, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (key) DO UPDATE SET
			source_record_key = EXCLUDED.source_record_key,
			state             = EXCLUDED.state,
			data              = EXCLUDED.data,
			updated_at        = EXCLUDED.updated_at
	`, pr.Key, pr.SourceRecordKey, pr.IntegrationID, pr.OrgID, pr.State, dataJSON, pr.CreatedAt, pr.UpdatedAt)
	if err != nil {
		return fmt.Errorf("SavePullRequest insert pr: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO pull_request_enrichments (pull_request_key, org_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, pr.Key, pr.OrgID)
	if err != nil {
		return fmt.Errorf("SavePullRequest insert enrichment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("SavePullRequest commit: %w", err)
	}
	return nil
}
