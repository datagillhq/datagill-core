package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// LaunchedWin is a single flag that reached 100% rollout within a query window.
type LaunchedWin struct {
	FlagKey           uuid.UUID
	FlagName          string
	ActorEmail        string
	ActorName         string
	RolloutPercentage float64
	OccurredAt        time.Time
}

// LaunchedWinsResult is the response from QueryLaunchedWins.
type LaunchedWinsResult struct {
	Wins []LaunchedWin
}

// TopTeam is a single team entry ranked by changelog activity.
type TopTeam struct {
	TeamName    string
	ChangeCount int
}

// TopTeamsResult is the response from QueryTopTeams.
type TopTeamsResult struct {
	Teams []TopTeam
}

// VelocityResult is the response from QueryVelocity.
type VelocityResult struct {
	AverageMinutes float64
	SampleSize     int
}

// AuditEntry is a single changelog row returned by QueryAuditLog.
type AuditEntry struct {
	OccurredAt   time.Time
	FlagName     string
	FlagKey      uuid.UUID
	ActorEmail   string
	ActorName    string
	EventType    string
	Environments []string
}

// AuditLogResult is the paginated response from QueryAuditLog.
type AuditLogResult struct {
	Total   int
	Entries []AuditEntry
}

// PullRequestAnalyticsRow is a pull request plus its optional enrichment data.
type PullRequestAnalyticsRow struct {
	Key              uuid.UUID
	State            models.PullRequestState
	Data             models.PullRequestData
	EnrichmentStatus models.PREnrichmentStatus
	EnrichmentData   *models.PullRequestEnrichmentData
	EnrichedAt       *time.Time
}

// PullRequestsResult is the paginated response from QueryPullRequests.
type PullRequestsResult struct {
	Total int
	Rows  []PullRequestAnalyticsRow
}

// QueryLaunchedWins returns one deduped production rollout event per flag in [from, to].
// "Launched" means the event-specific rollout was greater than 0%.
// Optional team and actor filters AND together when both are non-empty.
func (r *PostgresRepository) QueryLaunchedWins(ctx context.Context, orgID uuid.UUID, from, to time.Time, team, actor string) (LaunchedWinsResult, error) {
	const q = `
		WITH candidate_events AS (
			SELECT
				ff.key AS flag_key,
				ff.data->>'name' AS flag_name,
				COALESCE(ffc.data->'actor'->>'email', '') AS actor_email,
				COALESCE(ffc.data->'actor'->>'name',  '') AS actor_name,
				ffc.occurred_at,
				CASE
					WHEN sr.data->>'titleVerb' = 'turned on the flag' THEN 100::float8
					WHEN sr.data->>'titleVerb' = 'turned off the flag' THEN 0::float8
					WHEN COALESCE(ffc.data->>'description', '') ~* 'to\\s+[^[:alnum:]]*true[^[:alnum:]]*$' THEN 100::float8
					WHEN COALESCE(ffc.data->>'description', '') ~* 'to\\s+[^[:alnum:]]*false[^[:alnum:]]*$' THEN 0::float8
					WHEN substring(COALESCE(ffc.data->>'description', '') FROM '([0-9]+(?:\.[0-9]+)?)%[^%]*$') IS NOT NULL
						THEN substring(COALESCE(ffc.data->>'description', '') FROM '([0-9]+(?:\.[0-9]+)?)%[^%]*$')::float8
					ELSE NULL
				END AS rollout_percentage
			FROM feature_flag_changelog ffc
			JOIN feature_flags ff ON ff.key = ffc.feature_flag_key
			LEFT JOIN source_records sr ON sr.key = ffc.source_record_key
			WHERE ffc.org_id = $1
			  AND ffc.state  = 'active'
			  AND ffc.occurred_at >= $2 AND ffc.occurred_at <= $3
			  AND ffc.data->'environments' @> '["production"]'::jsonb
			  AND ($4 = '' OR ff.data->'maintainer'->'team'->>'name' = $4)
			  AND ($5 = '' OR ffc.data->'actor'->>'email' = $5)
		),
		deduped AS (
			SELECT
				flag_key,
				flag_name,
				actor_email,
				actor_name,
				rollout_percentage,
				occurred_at,
				ROW_NUMBER() OVER (
					PARTITION BY flag_key
					ORDER BY occurred_at DESC, rollout_percentage DESC NULLS LAST
				) AS rn
			FROM candidate_events
			WHERE rollout_percentage > 0
		)
		SELECT
			flag_key,
			flag_name,
			actor_email,
			actor_name,
			rollout_percentage,
			occurred_at
		FROM deduped
		WHERE rn = 1
		ORDER BY occurred_at DESC`

	rows, err := r.pool.Query(ctx, q, orgID, from, to, team, actor)
	if err != nil {
		return LaunchedWinsResult{}, fmt.Errorf("QueryLaunchedWins: %w", err)
	}
	defer rows.Close()

	var res LaunchedWinsResult
	for rows.Next() {
		var w LaunchedWin
		if err := rows.Scan(&w.FlagKey, &w.FlagName, &w.ActorEmail, &w.ActorName, &w.RolloutPercentage, &w.OccurredAt); err != nil {
			return LaunchedWinsResult{}, fmt.Errorf("QueryLaunchedWins scan: %w", err)
		}
		res.Wins = append(res.Wins, w)
	}
	if err := rows.Err(); err != nil {
		return LaunchedWinsResult{}, fmt.Errorf("QueryLaunchedWins rows: %w", err)
	}
	return res, nil
}

// QueryTopTeams returns teams ranked by number of flag changelog events in [from, to].
func (r *PostgresRepository) QueryTopTeams(ctx context.Context, orgID uuid.UUID, from, to time.Time) (TopTeamsResult, error) {
	const q = `
		SELECT
			ff.data->'maintainer'->'team'->>'name' AS team_name,
			COUNT(*)::int                           AS change_count
		FROM feature_flag_changelog ffc
		JOIN feature_flags ff ON ff.key = ffc.feature_flag_key
		WHERE ffc.org_id = $1
		  AND ffc.state  = 'active'
		  AND ffc.occurred_at >= $2 AND ffc.occurred_at <= $3
		  AND ff.data->'maintainer'->'team'->>'name' IS NOT NULL
		GROUP BY team_name
		ORDER BY change_count DESC`

	rows, err := r.pool.Query(ctx, q, orgID, from, to)
	if err != nil {
		return TopTeamsResult{}, fmt.Errorf("QueryTopTeams: %w", err)
	}
	defer rows.Close()

	var res TopTeamsResult
	for rows.Next() {
		var t TopTeam
		if err := rows.Scan(&t.TeamName, &t.ChangeCount); err != nil {
			return TopTeamsResult{}, fmt.Errorf("QueryTopTeams scan: %w", err)
		}
		res.Teams = append(res.Teams, t)
	}
	if err := rows.Err(); err != nil {
		return TopTeamsResult{}, fmt.Errorf("QueryTopTeams rows: %w", err)
	}
	return res, nil
}

// QueryVelocity returns the average time (minutes) from flag creation to reaching
// 100% rollout for flags that hit 100% within [from, to].
func (r *PostgresRepository) QueryVelocity(ctx context.Context, orgID uuid.UUID, from, to time.Time) (VelocityResult, error) {
	const q = `
		SELECT
			COALESCE(AVG(EXTRACT(EPOCH FROM (sub.first_at - ff.created_at)) / 60), 0)::float8,
			COUNT(*)::int
		FROM (
			SELECT ffc.feature_flag_key, MIN(ffc.occurred_at) AS first_at
			FROM feature_flag_changelog ffc
			JOIN feature_flags ff ON ff.key = ffc.feature_flag_key
			WHERE ffc.org_id = $1
			  AND ffc.state  = 'active'
			  AND ffc.occurred_at >= $2 AND ffc.occurred_at <= $3
			  AND EXISTS (
			        SELECT 1 FROM jsonb_each(ff.data->'environments') env(k, v)
			        WHERE (v->>'rollout_percentage')::numeric = 100
			      )
			GROUP BY ffc.feature_flag_key
		) sub
		JOIN feature_flags ff ON ff.key = sub.feature_flag_key`

	var res VelocityResult
	err := r.pool.QueryRow(ctx, q, orgID, from, to).Scan(&res.AverageMinutes, &res.SampleSize)
	if err != nil {
		return VelocityResult{}, fmt.Errorf("QueryVelocity: %w", err)
	}
	return res, nil
}

// auditLogFilter is the WHERE clause shared by the QueryAuditLog count and page queries.
const auditLogFilter = `
		FROM feature_flag_changelog ffc
		JOIN feature_flags ff ON ff.key = ffc.feature_flag_key
		WHERE ffc.org_id = $1
		  AND ffc.state  = 'active'
		  AND ffc.occurred_at >= $2 AND ffc.occurred_at <= $3
		  AND ($4 = '' OR ff.data->'maintainer'->'team'->>'name' = $4)
		  AND ($5 = '' OR ffc.data->'actor'->>'email' = $5)`

// QueryAuditLog returns a paginated changelog for an org within [from, to].
// Optional team and actor filters AND together when both are non-empty.
//
// Total is counted separately, like QueryPullRequests: a window count (COUNT(*) OVER())
// is only read from returned rows, so a page past the end reported a total of 0.
func (r *PostgresRepository) QueryAuditLog(ctx context.Context, orgID uuid.UUID, from, to time.Time, team, actor string, page, limit int) (AuditLogResult, error) {
	var result AuditLogResult
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*)::int`+auditLogFilter, orgID, from, to, team, actor).Scan(&result.Total); err != nil {
		return AuditLogResult{}, fmt.Errorf("QueryAuditLog count: %w", err)
	}

	const q = `
		SELECT
			ffc.occurred_at,
			ff.data->>'name',
			ff.key,
			COALESCE(ffc.data->'actor'->>'email', ''),
			COALESCE(ffc.data->'actor'->>'name',  ''),
			ffc.event_type,
			ffc.data->'environments'` + auditLogFilter + `
		ORDER BY ffc.occurred_at DESC, ffc.key DESC
		LIMIT $6 OFFSET $7`

	offset := (page - 1) * limit
	rows, err := r.pool.Query(ctx, q, orgID, from, to, team, actor, limit, offset)
	if err != nil {
		return AuditLogResult{}, fmt.Errorf("QueryAuditLog: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var e AuditEntry
		var envsJSON []byte
		if err := rows.Scan(
			&e.OccurredAt, &e.FlagName, &e.FlagKey,
			&e.ActorEmail, &e.ActorName, &e.EventType,
			&envsJSON,
		); err != nil {
			return AuditLogResult{}, fmt.Errorf("QueryAuditLog scan: %w", err)
		}
		if len(envsJSON) > 0 {
			if err := json.Unmarshal(envsJSON, &e.Environments); err != nil {
				return AuditLogResult{}, fmt.Errorf("QueryAuditLog unmarshal environments: %w", err)
			}
		}
		result.Entries = append(result.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return AuditLogResult{}, fmt.Errorf("QueryAuditLog rows: %w", err)
	}
	return result, nil
}

// QueryPullRequests returns merged GitHub pull requests for an org within [from, to],
// ordered by provider merged_at newest first. PRs without merged_at are excluded.
func (r *PostgresRepository) QueryPullRequests(ctx context.Context, orgID uuid.UUID, from, to time.Time, page, limit int) (PullRequestsResult, error) {
	const countQ = `
		SELECT COUNT(*)::int
		FROM pull_requests pr
		WHERE pr.org_id = $1
		  AND pr.state = 'merged'
		  AND NULLIF(pr.data->'source'->>'merged_at', '')::timestamptz >= $2
		  AND NULLIF(pr.data->'source'->>'merged_at', '')::timestamptz <= $3`

	var result PullRequestsResult
	if err := r.pool.QueryRow(ctx, countQ, orgID, from, to).Scan(&result.Total); err != nil {
		return PullRequestsResult{}, fmt.Errorf("QueryPullRequests count: %w", err)
	}

	const q = `
		SELECT
			pr.key,
			pr.state,
			pr.data,
			COALESCE(pre.status, 'pending') AS enrichment_status,
			pre.enrichment,
			pre.enriched_at
		FROM pull_requests pr
		LEFT JOIN pull_request_enrichments pre
		       ON pre.pull_request_key = pr.key
		      AND pre.org_id = pr.org_id
		WHERE pr.org_id = $1
		  AND pr.state = 'merged'
		  AND NULLIF(pr.data->'source'->>'merged_at', '')::timestamptz >= $2
		  AND NULLIF(pr.data->'source'->>'merged_at', '')::timestamptz <= $3
		ORDER BY NULLIF(pr.data->'source'->>'merged_at', '')::timestamptz DESC, pr.key DESC
		LIMIT $4 OFFSET $5`

	offset := (page - 1) * limit
	rows, err := r.pool.Query(ctx, q, orgID, from, to, limit, offset)
	if err != nil {
		return PullRequestsResult{}, fmt.Errorf("QueryPullRequests: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			row            PullRequestAnalyticsRow
			state          string
			dataJSON       []byte
			enrichmentJSON []byte
			status         string
		)
		if err := rows.Scan(
			&row.Key,
			&state,
			&dataJSON,
			&status,
			&enrichmentJSON,
			&row.EnrichedAt,
		); err != nil {
			return PullRequestsResult{}, fmt.Errorf("QueryPullRequests scan: %w", err)
		}
		if err := json.Unmarshal(dataJSON, &row.Data); err != nil {
			return PullRequestsResult{}, fmt.Errorf("QueryPullRequests unmarshal data: %w", err)
		}
		row.State = models.PullRequestState(state)
		row.EnrichmentStatus = models.PREnrichmentStatus(status)
		if enrichmentJSON != nil {
			var enrichment models.PullRequestEnrichmentData
			if err := json.Unmarshal(enrichmentJSON, &enrichment); err != nil {
				return PullRequestsResult{}, fmt.Errorf("QueryPullRequests unmarshal enrichment: %w", err)
			}
			row.EnrichmentData = &enrichment
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return PullRequestsResult{}, fmt.Errorf("QueryPullRequests rows: %w", err)
	}
	return result, nil
}
