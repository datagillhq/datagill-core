//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var testPGConnString string

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgContainer, err := pgmodule.Run(ctx,
		"postgres:16-alpine",
		pgmodule.WithDatabase("datagill_test"),
		pgmodule.WithUsername("test"),
		pgmodule.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		panic("start postgres container: " + err.Error())
	}

	testPGConnString, err = pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("get connection string: " + err.Error())
	}

	if err := runTestMigrations(ctx, testPGConnString); err != nil {
		panic("run migrations: " + err.Error())
	}

	code := m.Run()

	if err := pgContainer.Terminate(ctx); err != nil {
		panic("terminate container: " + err.Error())
	}

	os.Exit(code)
}

func runTestMigrations(ctx context.Context, connString string) error {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return err
	}
	defer pool.Close()

	dir := filepath.Join("..", "..", "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		sql, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return err
		}
	}
	return nil
}

// seedFlag inserts a feature_flag row directly, bypassing the ETL layer.
func seedFlag(t *testing.T, pool *pgxpool.Pool, orgID, flagKey, name, source string, envs []string) {
	t.Helper()
	environments := make(map[string]map[string]bool, len(envs))
	for _, e := range envs {
		environments[e] = map[string]bool{"enabled": true}
	}
	data, _ := json.Marshal(map[string]any{
		"name": name,
		"source": map[string]string{
			"provider": source,
			"key":      flagKey,
			"project":  "test-project",
		},
		"environments": environments,
		"lifecycle":    map[string]string{"purpose": "temporary", "state": "active"},
	})
	_, err := pool.Exec(context.Background(), `
		INSERT INTO feature_flags (key, source_record_key, integration_id, org_id, state, data, created_at, updated_at)
		SELECT gen_random_uuid(), gen_random_uuid(), i.key, $1::uuid, 'on', $2::jsonb, NOW(), NOW()
		FROM integrations i WHERE i.org_id = $1::uuid LIMIT 1
	`, orgID, string(data))
	if err != nil {
		t.Fatalf("seedFlag: %v", err)
	}
}

// seedOrg inserts an org + integration and returns the org UUID string.
func seedOrg(t *testing.T, pool *pgxpool.Pool, orgName string) string {
	t.Helper()
	var orgID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO organizations (key, external_org_id, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, NOW(), NOW()) RETURNING key::text
	`, orgName).Scan(&orgID)
	if err != nil {
		t.Fatalf("seedOrg: %v", err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO integrations (key, org_id, source, api_key, config, state, created_at, updated_at)
		VALUES (gen_random_uuid(), $1::uuid, 'launchdarkly', 'test-key', '{}', 'active', NOW(), NOW())
	`, orgID)
	if err != nil {
		t.Fatalf("seedOrg integration: %v", err)
	}
	// CASCADE deletes integrations, feature_flags, and changelog entries for this org.
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organizations WHERE key = $1::uuid`, orgID)
	})
	return orgID
}

func TestPostgresListFlags(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	orgA := seedOrg(t, pool, "org-a")
	orgB := seedOrg(t, pool, "org-b")

	seedFlag(t, pool, orgA, "flag-1", "Checkout Redesign", "launchdarkly", []string{"production", "staging"})
	seedFlag(t, pool, orgA, "flag-2", "Dark Mode", "unleash", []string{"staging"})
	seedFlag(t, pool, orgB, "flag-3", "Other Org Flag", "launchdarkly", []string{"production"})

	repo, err := NewPostgresSearchRepository(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	t.Run("returns only flags for the org", func(t *testing.T) {
		flags, err := repo.ListFlags(context.Background(), orgA, FlagFilters{})
		if err != nil {
			t.Fatal(err)
		}
		if len(flags) != 2 {
			t.Errorf("expected 2 flags for org-a, got %d", len(flags))
		}
	})

	t.Run("filters by source", func(t *testing.T) {
		flags, err := repo.ListFlags(context.Background(), orgA, FlagFilters{Source: "launchdarkly"})
		if err != nil {
			t.Fatal(err)
		}
		if len(flags) != 1 || flags[0].Name != "Checkout Redesign" {
			t.Errorf("expected 1 launchdarkly flag, got %+v", flags)
		}
	})

	t.Run("filters by environment", func(t *testing.T) {
		flags, err := repo.ListFlags(context.Background(), orgA, FlagFilters{Environment: "production"})
		if err != nil {
			t.Fatal(err)
		}
		if len(flags) != 1 || flags[0].Name != "Checkout Redesign" {
			t.Errorf("expected 1 production flag for org-a, got %+v", flags)
		}
	})

	t.Run("org isolation — org-b cannot see org-a flags", func(t *testing.T) {
		flags, err := repo.ListFlags(context.Background(), orgB, FlagFilters{})
		if err != nil {
			t.Fatal(err)
		}
		if len(flags) != 1 || flags[0].Name != "Other Org Flag" {
			t.Errorf("expected only org-b flag, got %+v", flags)
		}
	})
}
