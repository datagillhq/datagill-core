package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// enrichmentMaxAttempts is how many failed attempts a PR gets before it is marked failed.
// With the backoff below, the 10th failure lands about 4.3 days after the first, so a
// multi-day GitHub outage does not permanently fail PRs.
const enrichmentMaxAttempts = 10

// After each failure the next attempt waits enrichmentBackoffBase, doubling per failure,
// capped at enrichmentBackoffMax: 30m, 1h, 2h, 4h, 8h, 16h, then 24h. These are minimum
// waits; the retry happens on the first scheduled run after the wait.
const (
	enrichmentBackoffBase = 30 * time.Minute
	enrichmentBackoffMax  = 24 * time.Hour
)

// enrichmentBackoff is the SQL for next_attempt_at after a failure. $3 is
// enrichmentBackoffBase and $4 is enrichmentBackoffMax, both in seconds. attempts is at
// least 1 here, since the row was claimed before it failed.
const enrichmentBackoff = `NOW() + LEAST(
		make_interval(secs => $3 * POWER(2, GREATEST(attempts, 1) - 1)),
		make_interval(secs => $4))`

// enrichmentStaleAfterSeconds is how long a row may sit in 'in_progress' before it is
// treated as abandoned (e.g. the worker died mid-run) and becomes claimable again.
// It must comfortably exceed the worst-case time to enrich one PR, including retries.
const enrichmentStaleAfterSeconds = 60 * 60

// claimableEnrichment matches rows a worker may claim: pending rows whose retry backoff has
// passed, plus in_progress rows whose claim has gone stale. $2 is enrichmentStaleAfterSeconds.
const claimableEnrichment = `((status = 'pending' AND next_attempt_at <= NOW()) OR (status = 'in_progress' AND updated_at < NOW() - make_interval(secs => $2)))`

// ListPendingEnrichments returns up to limit claimable enrichment rows for an org,
// ordered oldest first. Rows whose PR belongs to a paused or deleted integration are
// skipped and stay pending, so enrichment resumes when the integration is active again.
func (r *PostgresRepository) ListPendingEnrichments(ctx context.Context, orgID uuid.UUID, limit int) ([]models.PullRequestEnrichment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT pull_request_key, org_id, status, attempts, enrichment, enriched_at, created_at, updated_at
		FROM pull_request_enrichments
		WHERE org_id = $1
		  AND `+claimableEnrichment+`
		  AND EXISTS (
		      SELECT 1
		      FROM pull_requests pr
		      JOIN integrations i ON i.key = pr.integration_id
		      WHERE pr.key = pull_request_enrichments.pull_request_key
		        AND i.state = 'active'
		  )
		ORDER BY created_at ASC
		LIMIT $3
	`, orgID, enrichmentStaleAfterSeconds, limit)
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

// ClaimEnrichment sets status = 'in_progress' and increments attempts for a claimable row.
// It reports false when the row was not claimable (already claimed, completed, or failed),
// so the caller can skip it instead of racing another run.
func (r *PostgresRepository) ClaimEnrichment(ctx context.Context, orgID, pullRequestKey uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE pull_request_enrichments
		SET status     = 'in_progress',
		    attempts   = attempts + 1,
		    updated_at = NOW()
		WHERE org_id = $1
		  AND pull_request_key = $3
		  AND `+claimableEnrichment+`
	`, orgID, enrichmentStaleAfterSeconds, pullRequestKey)
	if err != nil {
		return false, fmt.Errorf("ClaimEnrichment: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SaveEnrichmentResult writes the enrichment output and marks the row completed.
// Only an in_progress row is updated, so a stale run cannot overwrite a finished row.
func (r *PostgresRepository) SaveEnrichmentResult(ctx context.Context, orgID, pullRequestKey uuid.UUID, data models.PullRequestEnrichmentData) error {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("SaveEnrichmentResult marshal: %w", err)
	}

	now := time.Now().UTC()
	_, err = r.pool.Exec(ctx, `
		UPDATE pull_request_enrichments
		SET enrichment  = $3,
		    enriched_at = $4,
		    status      = 'completed',
		    updated_at  = $4
		WHERE org_id = $1
		  AND pull_request_key = $2
		  AND status = 'in_progress'
	`, orgID, pullRequestKey, dataJSON, now)
	if err != nil {
		return fmt.Errorf("SaveEnrichmentResult: %w", err)
	}
	return nil
}

// MarkEnrichmentFailed resets the row to 'pending' and schedules the next attempt with
// backoff, or sets 'failed' if the attempt cap has been reached. Only an in_progress row
// is updated.
func (r *PostgresRepository) MarkEnrichmentFailed(ctx context.Context, orgID, pullRequestKey uuid.UUID) error {
	// $3 and $4 are fixed by enrichmentBackoff, so the org is $5.
	_, err := r.pool.Exec(ctx, `
		UPDATE pull_request_enrichments
		SET status          = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'pending' END,
		    next_attempt_at = `+enrichmentBackoff+`,
		    updated_at      = NOW()
		WHERE org_id = $5
		  AND pull_request_key = $1
		  AND status = 'in_progress'
	`, pullRequestKey, enrichmentMaxAttempts, enrichmentBackoffBase.Seconds(), enrichmentBackoffMax.Seconds(), orgID)
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
