package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

const enrichmentMaxAttempts = 5

// SavePullRequest upserts the normalized state of a pull request.
// Enrichment columns are not touched on upsert — ETL owns data, enrichment workflow owns enrichment.
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
			state             = EXCLUDED.state,
			data              = EXCLUDED.data,
			updated_at        = EXCLUDED.updated_at
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

// ListPendingPRsForEnrichment returns up to limit pull request rows with status = 'pending',
// ordered oldest first.
func (r *PostgresRepository) ListPendingPRsForEnrichment(ctx context.Context, limit int) ([]models.PullRequest, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT key, source_record_key, integration_id, org_id, state, data,
		       enrichment, enriched_at, status, attempts, created_at, updated_at
		FROM pull_requests
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("ListPendingPRsForEnrichment: %w", err)
	}
	defer rows.Close()

	var prs []models.PullRequest
	for rows.Next() {
		pr, err := scanPullRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("ListPendingPRsForEnrichment scan: %w", err)
		}
		prs = append(prs, pr)
	}
	return prs, rows.Err()
}

// ClaimPRForEnrichment sets status = 'in_progress' and increments attempts for a single PR.
// No-op if the row is already in_progress or completed.
func (r *PostgresRepository) ClaimPRForEnrichment(ctx context.Context, key uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE pull_requests
		SET status   = 'in_progress',
		    attempts = attempts + 1
		WHERE key = $1
		  AND status = 'pending'
	`, key)
	if err != nil {
		return fmt.Errorf("ClaimPRForEnrichment: %w", err)
	}
	return nil
}

// SaveEnrichmentResult writes the enrichment output and marks the PR completed.
func (r *PostgresRepository) SaveEnrichmentResult(ctx context.Context, key uuid.UUID, enrichment models.PullRequestEnrichment) error {
	enrichmentJSON, err := json.Marshal(enrichment)
	if err != nil {
		return fmt.Errorf("SaveEnrichmentResult marshal: %w", err)
	}

	now := time.Now().UTC()
	_, err = r.pool.Exec(ctx, `
		UPDATE pull_requests
		SET enrichment  = $2,
		    enriched_at = $3,
		    status      = 'completed'
		WHERE key = $1
	`, key, enrichmentJSON, now)
	if err != nil {
		return fmt.Errorf("SaveEnrichmentResult: %w", err)
	}
	return nil
}

// MarkEnrichmentFailed increments attempts and either resets to 'pending' for retry
// or sets 'failed' if the retry cap is reached.
func (r *PostgresRepository) MarkEnrichmentFailed(ctx context.Context, key uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE pull_requests
		SET status = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'pending' END
		WHERE key = $1
	`, key, enrichmentMaxAttempts)
	if err != nil {
		return fmt.Errorf("MarkEnrichmentFailed: %w", err)
	}
	return nil
}

// rowScanner is satisfied by pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPullRequest(row rowScanner) (models.PullRequest, error) {
	var (
		pr           models.PullRequest
		dataJSON     []byte
		enrichJSON   []byte
		enrichedAt   *time.Time
		status       string
	)

	err := row.Scan(
		&pr.Key,
		&pr.SourceRecordKey,
		&pr.IntegrationID,
		&pr.OrgID,
		&pr.State,
		&dataJSON,
		&enrichJSON,
		&enrichedAt,
		&status,
		&pr.Attempts,
		&pr.CreatedAt,
		&pr.UpdatedAt,
	)
	if err != nil {
		return models.PullRequest{}, err
	}

	if err := json.Unmarshal(dataJSON, &pr.Data); err != nil {
		return models.PullRequest{}, fmt.Errorf("unmarshal data: %w", err)
	}

	if enrichJSON != nil {
		var e models.PullRequestEnrichment
		if err := json.Unmarshal(enrichJSON, &e); err != nil {
			return models.PullRequest{}, fmt.Errorf("unmarshal enrichment: %w", err)
		}
		pr.Enrichment = &e
	}

	pr.EnrichedAt = enrichedAt
	pr.Status = models.PREnrichmentStatus(status)

	return pr, nil
}
