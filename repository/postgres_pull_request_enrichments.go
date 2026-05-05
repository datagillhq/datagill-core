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

// ListPendingEnrichments returns up to limit enrichment rows with status = 'pending' for an org,
// ordered oldest first.
func (r *PostgresRepository) ListPendingEnrichments(ctx context.Context, orgID uuid.UUID, limit int) ([]models.PullRequestEnrichment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT pull_request_key, org_id, status, attempts, enrichment, enriched_at, created_at, updated_at
		FROM pull_request_enrichments
		WHERE status = 'pending'
		  AND org_id = $1
		ORDER BY created_at ASC
		LIMIT $2
	`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("ListPendingEnrichments: %w", err)
	}
	defer rows.Close()

	var results []models.PullRequestEnrichment
	for rows.Next() {
		e, err := scanEnrichment(rows)
		if err != nil {
			return nil, fmt.Errorf("ListPendingEnrichments scan: %w", err)
		}
		results = append(results, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListPendingEnrichments rows: %w", err)
	}
	return results, nil
}

// ClaimEnrichment sets status = 'in_progress' and increments attempts for a pending row.
// No-op if the row is already in_progress, completed, or failed.
func (r *PostgresRepository) ClaimEnrichment(ctx context.Context, pullRequestKey uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE pull_request_enrichments
		SET status     = 'in_progress',
		    attempts   = attempts + 1,
		    updated_at = NOW()
		WHERE pull_request_key = $1
		  AND status = 'pending'
	`, pullRequestKey)
	if err != nil {
		return fmt.Errorf("ClaimEnrichment: %w", err)
	}
	return nil
}

// SaveEnrichmentResult writes the enrichment output and marks the row completed.
func (r *PostgresRepository) SaveEnrichmentResult(ctx context.Context, pullRequestKey uuid.UUID, data models.PullRequestEnrichmentData) error {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("SaveEnrichmentResult marshal: %w", err)
	}

	now := time.Now().UTC()
	_, err = r.pool.Exec(ctx, `
		UPDATE pull_request_enrichments
		SET enrichment  = $2,
		    enriched_at = $3,
		    status      = 'completed',
		    updated_at  = $3
		WHERE pull_request_key = $1
	`, pullRequestKey, dataJSON, now)
	if err != nil {
		return fmt.Errorf("SaveEnrichmentResult: %w", err)
	}
	return nil
}

// MarkEnrichmentFailed resets the row to 'pending' for retry, or sets 'failed' if
// the attempt cap has been reached.
func (r *PostgresRepository) MarkEnrichmentFailed(ctx context.Context, pullRequestKey uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE pull_request_enrichments
		SET status     = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'pending' END,
		    updated_at = NOW()
		WHERE pull_request_key = $1
	`, pullRequestKey, enrichmentMaxAttempts)
	if err != nil {
		return fmt.Errorf("MarkEnrichmentFailed: %w", err)
	}
	return nil
}

type enrichmentRowScanner interface {
	Scan(dest ...any) error
}

func scanEnrichment(row enrichmentRowScanner) (models.PullRequestEnrichment, error) {
	var (
		e          models.PullRequestEnrichment
		dataJSON   []byte
		status     string
		enrichedAt *time.Time
	)
	err := row.Scan(
		&e.PullRequestKey,
		&e.OrgID,
		&status,
		&e.Attempts,
		&dataJSON,
		&enrichedAt,
		&e.CreatedAt,
		&e.UpdatedAt,
	)
	if err != nil {
		return models.PullRequestEnrichment{}, err
	}

	e.Status = models.PREnrichmentStatus(status)
	e.EnrichedAt = enrichedAt

	if dataJSON != nil {
		var d models.PullRequestEnrichmentData
		if err := json.Unmarshal(dataJSON, &d); err != nil {
			return models.PullRequestEnrichment{}, fmt.Errorf("unmarshal enrichment data: %w", err)
		}
		e.Data = &d
	}

	return e, nil
}
