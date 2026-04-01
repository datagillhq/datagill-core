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

const integrationColumns = `id, key, org_id, source, api_key, config, state, created_at, updated_at`

func scanIntegration(row pgx.Row) (models.Integration, error) {
	var i models.Integration
	var configJSON []byte
	err := row.Scan(&i.ID, &i.Key, &i.OrgID, &i.Source, &i.APIKey, &configJSON, &i.State, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return models.Integration{}, err
	}
	if err := json.Unmarshal(configJSON, &i.Config); err != nil {
		return models.Integration{}, fmt.Errorf("unmarshal config: %w", err)
	}
	return i, nil
}

// EnsureOrg inserts an org row if one does not already exist.
func (r *PostgresRepository) EnsureOrg(ctx context.Context, orgID uuid.UUID, externalOrgID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO organizations (key, external_org_id, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (key) DO NOTHING
	`, orgID, externalOrgID)
	if err != nil {
		return fmt.Errorf("EnsureOrg: %w", err)
	}
	return nil
}

// CreateIntegration inserts a new integration and returns it with DB-assigned fields.
func (r *PostgresRepository) CreateIntegration(ctx context.Context, i models.Integration) (models.Integration, error) {
	configJSON, err := json.Marshal(i.Config)
	if err != nil {
		return models.Integration{}, fmt.Errorf("CreateIntegration marshal config: %w", err)
	}

	if i.Key == uuid.Nil {
		i.Key = uuid.New()
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'active', NOW(), NOW())
		RETURNING `+integrationColumns,
		i.Key, i.OrgID, i.Source, i.APIKey, configJSON,
	)
	created, err := scanIntegration(row)
	if err != nil {
		return models.Integration{}, fmt.Errorf("CreateIntegration: %w", err)
	}
	return created, nil
}

// GetIntegration returns a single non-deleted integration by key, scoped to the org.
func (r *PostgresRepository) GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+integrationColumns+`
		FROM integrations
		WHERE org_id = $1 AND key = $2 AND state != 'deleted'
	`, orgID, key)
	i, err := scanIntegration(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Integration{}, ErrNotFound
		}
		return models.Integration{}, fmt.Errorf("GetIntegration: %w", err)
	}
	return i, nil
}

// ListIntegrations returns all active and paused integrations for an org.
func (r *PostgresRepository) ListIntegrations(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+integrationColumns+`
		FROM integrations
		WHERE org_id = $1 AND state IN ('active', 'paused')
		ORDER BY created_at
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("ListIntegrations query: %w", err)
	}
	defer rows.Close()

	var integrations []models.Integration
	for rows.Next() {
		i, err := scanIntegration(rows)
		if err != nil {
			return nil, fmt.Errorf("ListIntegrations scan: %w", err)
		}
		integrations = append(integrations, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListIntegrations rows: %w", err)
	}
	return integrations, nil
}

// UpdateIntegration applies a partial update to an integration scoped to the org.
func (r *PostgresRepository) UpdateIntegration(ctx context.Context, orgID, key uuid.UUID, patch models.IntegrationPatch) (models.Integration, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE integrations
		SET
			api_key    = COALESCE($3, api_key),
			state      = COALESCE($4, state),
			updated_at = NOW()
		WHERE org_id = $1 AND key = $2 AND state != 'deleted'
		RETURNING `+integrationColumns,
		orgID, key, patch.APIKey, patch.State,
	)
	i, err := scanIntegration(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Integration{}, ErrNotFound
		}
		return models.Integration{}, fmt.Errorf("UpdateIntegration: %w", err)
	}
	return i, nil
}

// SoftDeleteIntegration sets state = deleted on the integration, scoped to the org.
func (r *PostgresRepository) SoftDeleteIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE integrations
		SET state = 'deleted', updated_at = NOW()
		WHERE org_id = $1 AND key = $2 AND state != 'deleted'
		RETURNING `+integrationColumns,
		orgID, key,
	)
	i, err := scanIntegration(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Integration{}, ErrNotFound
		}
		return models.Integration{}, fmt.Errorf("SoftDeleteIntegration: %w", err)
	}
	return i, nil
}
