package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/datagill/datagill-core/models"
)

const integrationColumns = `id, key, org_id, source, api_key, config, state, last_synced_at, created_at, updated_at`

// rowToIntegration scans a pgx.Row into an Integration, decrypting the API key.
func (r *PostgresRepository) rowToIntegration(row pgx.Row) (models.Integration, error) {
	var i models.Integration
	var configJSON []byte
	var encryptedAPIKey string
	err := row.Scan(&i.ID, &i.Key, &i.OrgID, &i.Source, &encryptedAPIKey, &configJSON, &i.State, &i.LastSyncedAt, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return models.Integration{}, fmt.Errorf("scan: %w", err)
	}
	if err := json.Unmarshal(configJSON, &i.Config); err != nil {
		return models.Integration{}, fmt.Errorf("unmarshal config: %w", err)
	}
	apiKey, err := decryptAPIKey(r.encryptionKey, encryptedAPIKey)
	if err != nil {
		return models.Integration{}, fmt.Errorf("decrypt api_key: %w", err)
	}
	i.APIKey = apiKey
	return i, nil
}

// UpdateIntegrationLastSyncedAt records the high-water mark after a successful sync.
func (r *PostgresRepository) UpdateIntegrationLastSyncedAt(ctx context.Context, key uuid.UUID, syncedAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE integrations
		SET last_synced_at = $2, updated_at = NOW()
		WHERE key = $1 AND state != 'deleted'
	`, key, syncedAt)
	if err != nil {
		return fmt.Errorf("UpdateIntegrationLastSyncedAt: %w", err)
	}
	return nil
}

// CreateOrg inserts an org row if one does not already exist.
// Safe to call multiple times — uses INSERT ... ON CONFLICT DO NOTHING.
func (r *PostgresRepository) CreateOrg(ctx context.Context, orgID uuid.UUID, externalOrgID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO organizations (key, external_org_id, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (key) DO NOTHING
	`, orgID, externalOrgID)
	if err != nil {
		return fmt.Errorf("CreateOrg: %w", err)
	}
	return nil
}

// CreateIntegration inserts a new integration and returns it with DB-assigned fields.
func (r *PostgresRepository) CreateIntegration(ctx context.Context, i models.Integration) (models.Integration, error) {
	configJSON, err := json.Marshal(i.Config)
	if err != nil {
		return models.Integration{}, fmt.Errorf("CreateIntegration marshal config: %w", err)
	}

	encryptedKey, err := encryptAPIKey(r.encryptionKey, i.APIKey)
	if err != nil {
		return models.Integration{}, fmt.Errorf("CreateIntegration encrypt api_key: %w", err)
	}

	if i.Key == uuid.Nil {
		i.Key = uuid.New()
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'active', NOW(), NOW())
		RETURNING `+integrationColumns,
		i.Key, i.OrgID, i.Source, encryptedKey, configJSON,
	)
	created, err := r.rowToIntegration(row)
	if err != nil {
		return models.Integration{}, fmt.Errorf("CreateIntegration: %w", err)
	}
	return created, nil
}

// UpsertGitHubIntegration upserts an active GitHub integration for the given org and installation.
// Deleted rows are treated as tombstones: a new active row is inserted instead of mutating them.
func (r *PostgresRepository) UpsertGitHubIntegration(ctx context.Context, orgID uuid.UUID, config models.IntegrationConfig) (models.Integration, bool, error) {
	if config.InstallationID == 0 {
		return models.Integration{}, false, fmt.Errorf("UpsertGitHubIntegration: installation_id is required")
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return models.Integration{}, false, fmt.Errorf("UpsertGitHubIntegration marshal config: %w", err)
	}

	encryptedKey, err := encryptAPIKey(r.encryptionKey, "")
	if err != nil {
		return models.Integration{}, false, fmt.Errorf("UpsertGitHubIntegration encrypt api_key: %w", err)
	}

	installationID := strconv.FormatInt(config.InstallationID, 10)

	row := r.pool.QueryRow(ctx, `
		UPDATE integrations
		SET
			config = $3,
			state = 'active',
			updated_at = NOW()
		WHERE org_id = $1
		  AND source = 'github'
		  AND state IN ('active', 'paused')
		  AND config->>'installation_id' = $2
		RETURNING `+integrationColumns,
		orgID, installationID, configJSON,
	)

	updated, err := r.rowToIntegration(row)
	if err == nil {
		return updated, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.Integration{}, false, fmt.Errorf("UpsertGitHubIntegration update: %w", err)
	}

	key := uuid.New()
	insertRow := r.pool.QueryRow(ctx, `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES ($1, $2, 'github', $3, $4, 'active', NOW(), NOW())
		RETURNING `+integrationColumns,
		key, orgID, encryptedKey, configJSON,
	)

	created, err := r.rowToIntegration(insertRow)
	if err != nil {
		return models.Integration{}, false, fmt.Errorf("UpsertGitHubIntegration insert: %w", err)
	}
	return created, true, nil
}

// GetIntegration returns a single non-deleted integration by key, scoped to the org.
func (r *PostgresRepository) GetIntegration(ctx context.Context, orgID, key uuid.UUID) (models.Integration, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+integrationColumns+`
		FROM integrations
		WHERE org_id = $1 AND key = $2 AND state != 'deleted'
	`, orgID, key)
	i, err := r.rowToIntegration(row)
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
		i, err := r.rowToIntegration(rows)
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
	var encryptedKey *string
	if patch.APIKey != nil {
		ek, err := encryptAPIKey(r.encryptionKey, *patch.APIKey)
		if err != nil {
			return models.Integration{}, fmt.Errorf("UpdateIntegration encrypt api_key: %w", err)
		}
		encryptedKey = &ek
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE integrations
		SET
			api_key    = COALESCE($3, api_key),
			state      = COALESCE($4, state),
			updated_at = NOW()
		WHERE org_id = $1 AND key = $2 AND state != 'deleted'
		RETURNING `+integrationColumns,
		orgID, key, encryptedKey, patch.State,
	)
	i, err := r.rowToIntegration(row)
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
	i, err := r.rowToIntegration(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Integration{}, ErrNotFound
		}
		return models.Integration{}, fmt.Errorf("SoftDeleteIntegration: %w", err)
	}
	return i, nil
}
