package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// LaunchedWin is a single flag that reached 100% rollout within a query window.
type LaunchedWin struct {
	FlagKey    uuid.UUID
	FlagName   string
	ActorEmail string
	ActorName  string
	OccurredAt time.Time
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

// QueryLaunchedWins returns flags that had a changelog event in [from, to] and
// currently have at least one environment at 100% rollout.
// Optional team and actor filters AND together when both are non-empty.
func (r *PostgresRepository) QueryLaunchedWins(ctx context.Context, orgID uuid.UUID, from, to time.Time, team, actor string) (LaunchedWinsResult, error) {
	const q = `
		SELECT
			ff.key,
			ff.data->>'name',
			COALESCE(ffc.data->'actor'->>'email', ''),
			COALESCE(ffc.data->'actor'->>'name',  ''),
			ffc.occurred_at
		FROM feature_flag_changelog ffc
		JOIN feature_flags ff ON ff.key = ffc.feature_flag_key
		WHERE ffc.org_id = $1
		  AND ffc.state  = 'active'
		  AND ffc.occurred_at >= $2 AND ffc.occurred_at <= $3
		  AND EXISTS (
		        SELECT 1 FROM jsonb_each(ff.data->'environments') env(k, v)
		        WHERE (v->>'rollout_percentage')::numeric = 100
		      )
		  AND ($4 = '' OR ff.data->'maintainer'->'team'->>'name' = $4)
		  AND ($5 = '' OR ffc.data->'actor'->>'email' = $5)
		ORDER BY ffc.occurred_at DESC`

	rows, err := r.pool.Query(ctx, q, orgID, from, to, team, actor)
	if err != nil {
		return LaunchedWinsResult{}, fmt.Errorf("QueryLaunchedWins: %w", err)
	}
	defer rows.Close()

	var res LaunchedWinsResult
	for rows.Next() {
		var w LaunchedWin
		if err := rows.Scan(&w.FlagKey, &w.FlagName, &w.ActorEmail, &w.ActorName, &w.OccurredAt); err != nil {
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

// QueryAuditLog returns a paginated changelog for an org within [from, to].
// Optional team and actor filters AND together when both are non-empty.
func (r *PostgresRepository) QueryAuditLog(ctx context.Context, orgID uuid.UUID, from, to time.Time, team, actor string, page, limit int) (AuditLogResult, error) {
	const q = `
		SELECT
			ffc.occurred_at,
			ff.data->>'name',
			ff.key,
			COALESCE(ffc.data->'actor'->>'email', ''),
			COALESCE(ffc.data->'actor'->>'name',  ''),
			ffc.event_type,
			ffc.data->'environments',
			COUNT(*) OVER() AS total
		FROM feature_flag_changelog ffc
		JOIN feature_flags ff ON ff.key = ffc.feature_flag_key
		WHERE ffc.org_id = $1
		  AND ffc.state  = 'active'
		  AND ffc.occurred_at >= $2 AND ffc.occurred_at <= $3
		  AND ($4 = '' OR ff.data->'maintainer'->'team'->>'name' = $4)
		  AND ($5 = '' OR ffc.data->'actor'->>'email' = $5)
		ORDER BY ffc.occurred_at DESC
		LIMIT $6 OFFSET $7`

	offset := (page - 1) * limit
	rows, err := r.pool.Query(ctx, q, orgID, from, to, team, actor, limit, offset)
	if err != nil {
		return AuditLogResult{}, fmt.Errorf("QueryAuditLog: %w", err)
	}
	defer rows.Close()

	var result AuditLogResult
	for rows.Next() {
		var e AuditEntry
		var envsJSON []byte
		if err := rows.Scan(
			&e.OccurredAt, &e.FlagName, &e.FlagKey,
			&e.ActorEmail, &e.ActorName, &e.EventType,
			&envsJSON, &result.Total,
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
