package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/datagill/datagill-core/models"
)

// GetIntegration returns a single integration by its key.
func (r *PostgresRepository) GetIntegration(ctx context.Context, integrationKey uuid.UUID) (models.Integration, error) {
	query := `
		SELECT id, key, org_id, source, api_key, config, state, created_at, updated_at
		FROM integrations
		WHERE key = $1
	`

	var i models.Integration
	var configJSON []byte

	err := r.pool.QueryRow(ctx, query, integrationKey).Scan(
		&i.ID,
		&i.Key,
		&i.OrgID,
		&i.Source,
		&i.APIKey,
		&configJSON,
		&i.State,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Integration{}, ErrNotFound
		}
		return models.Integration{}, fmt.Errorf("GetIntegration: %w", err)
	}

	if err := json.Unmarshal(configJSON, &i.Config); err != nil {
		return models.Integration{}, fmt.Errorf("GetIntegration unmarshal config: %w", err)
	}

	return i, nil
}

// ListIntegrations returns all active integrations for an org.
func (r *PostgresRepository) ListIntegrations(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error) {
	query := `
		SELECT id, key, org_id, source, api_key, config, state, created_at, updated_at
		FROM integrations
		WHERE org_id = $1 AND state = 'active'
		ORDER BY created_at
	`

	rows, err := r.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("ListIntegrations query: %w", err)
	}
	defer rows.Close()

	var integrations []models.Integration
	for rows.Next() {
		var i models.Integration
		var configJSON []byte

		if err := rows.Scan(
			&i.ID,
			&i.Key,
			&i.OrgID,
			&i.Source,
			&i.APIKey,
			&configJSON,
			&i.State,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("ListIntegrations scan: %w", err)
		}

		if err := json.Unmarshal(configJSON, &i.Config); err != nil {
			return nil, fmt.Errorf("ListIntegrations unmarshal config: %w", err)
		}

		integrations = append(integrations, i)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListIntegrations rows: %w", err)
	}

	return integrations, nil
}
