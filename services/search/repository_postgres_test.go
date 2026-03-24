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

// seedFlag inserts a feature_flag row and returns the generated key.
func seedFlag(t *testing.T, pool *pgxpool.Pool, orgID, flagName, source string, envs []string) string {
	t.Helper()
	environments := make(map[string]map[string]bool, len(envs))
	for _, e := range envs {
		environments[e] = map[string]bool{"enabled": true}
	}
	data, err := json.Marshal(map[string]any{
		"name": flagName,
		"source": map[string]string{
			"provider": source,
			"key":      flagName,
			"project":  "test-project",
		},
		"environments": environments,
		"lifecycle":    map[string]string{"purpose": "temporary", "state": "active"},
	})
	if err != nil {
		t.Fatalf("seedFlag marshal: %v", err)
	}
	var key string
	if err = pool.QueryRow(context.Background(), `
		INSERT INTO feature_flags (key, source_record_key, integration_id, org_id, state, data, created_at, updated_at)
		SELECT gen_random_uuid(), gen_random_uuid(), i.key, $1::uuid, 'on', $2::jsonb, NOW(), NOW()
		FROM integrations i WHERE i.org_id = $1::uuid LIMIT 1
		RETURNING key::text
	`, orgID, string(data)).Scan(&key); err != nil {
		t.Fatalf("seedFlag: %v", err)
	}
	return key
}

// seedChangelog inserts a changelog entry for the given flag key.
func seedChangelog(t *testing.T, pool *pgxpool.Pool, orgID, flagKey, eventType, environment string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"environment": environment,
		"actor":       map[string]any{"email": "test@example.com", "is_automated": false},
		"is_rollback": false,
	})
	if err != nil {
		t.Fatalf("seedChangelog marshal: %v", err)
	}
	if _, err = pool.Exec(context.Background(), `
		INSERT INTO feature_flag_changelog
			(key, source_record_key, feature_flag_key, integration_id, org_id, event_type, state, occurred_at, data, created_at, updated_at)
		SELECT gen_random_uuid(), gen_random_uuid(), $2::uuid, i.key, $1::uuid, $3, 'on', NOW(), $4::jsonb, NOW(), NOW()
		FROM integrations i WHERE i.org_id = $1::uuid LIMIT 1
	`, orgID, flagKey, eventType, string(data)); err != nil {
		t.Fatalf("seedChangelog: %v", err)
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
		if _, err := pool.Exec(context.Background(), `DELETE FROM organizations WHERE key = $1::uuid`, orgID); err != nil {
			t.Errorf("cleanup seedOrg: %v", err)
		}
	})
	return orgID
}

func TestPostgresListFlags(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	orgA := seedOrg(t, pool, "org-a")
	orgB := seedOrg(t, pool, "org-b")

	seedFlag(t, pool, orgA, "Checkout Redesign", "launchdarkly", []string{"production", "staging"})
	seedFlag(t, pool, orgA, "Dark Mode", "unleash", []string{"staging"})
	seedFlag(t, pool, orgB, "Other Org Flag", "launchdarkly", []string{"production"})

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

func TestPostgresGetFlag(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	org := seedOrg(t, pool, "org-getflag")
	flagKey := seedFlag(t, pool, org, "My Flag", "launchdarkly", []string{"production"})

	repo, err := NewPostgresSearchRepository(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	t.Run("returns flag detail", func(t *testing.T) {
		flag, err := repo.GetFlag(context.Background(), org, flagKey)
		if err != nil {
			t.Fatal(err)
		}
		if flag.FlagKey != flagKey || flag.Name != "My Flag" || flag.Source != "launchdarkly" {
			t.Errorf("unexpected flag detail: %+v", flag)
		}
	})

	t.Run("returns ErrNotFound for unknown key", func(t *testing.T) {
		_, err := repo.GetFlag(context.Background(), org, "00000000-0000-0000-0000-000000000000")
		if err != ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("org isolation — wrong org cannot see flag", func(t *testing.T) {
		otherOrg := seedOrg(t, pool, "org-getflag-other")
		_, err := repo.GetFlag(context.Background(), otherOrg, flagKey)
		if err != ErrNotFound {
			t.Errorf("expected ErrNotFound for wrong org, got %v", err)
		}
	})
}

func TestPostgresGetFlagHistory(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	org := seedOrg(t, pool, "org-history")
	flagKey := seedFlag(t, pool, org, "History Flag", "launchdarkly", []string{"production"})
	seedChangelog(t, pool, org, flagKey, "flag_toggled_on", "production")
	seedChangelog(t, pool, org, flagKey, "flag_toggled_off", "production")
	seedChangelog(t, pool, org, flagKey, "flag_updated", "staging")

	repo, err := NewPostgresSearchRepository(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	t.Run("returns all entries for flag", func(t *testing.T) {
		entries, err := repo.GetFlagHistory(context.Background(), org, flagKey, Pagination{Page: 1, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3 {
			t.Errorf("expected 3 entries, got %d", len(entries))
		}
	})

	t.Run("pagination limits results", func(t *testing.T) {
		entries, err := repo.GetFlagHistory(context.Background(), org, flagKey, Pagination{Page: 1, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Errorf("expected 2 entries on page 1, got %d", len(entries))
		}
		page2, err := repo.GetFlagHistory(context.Background(), org, flagKey, Pagination{Page: 2, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page2) != 1 {
			t.Errorf("expected 1 entry on page 2, got %d", len(page2))
		}
	})
}

func TestPostgresGetTimeline(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	org := seedOrg(t, pool, "org-timeline")
	flagA := seedFlag(t, pool, org, "Flag A", "launchdarkly", []string{"production"})
	flagB := seedFlag(t, pool, org, "Flag B", "launchdarkly", []string{"production"})
	otherOrg := seedOrg(t, pool, "org-timeline-other")
	otherFlag := seedFlag(t, pool, otherOrg, "Other Flag", "launchdarkly", []string{"production"})

	seedChangelog(t, pool, org, flagA, "flag_toggled_on", "production")
	seedChangelog(t, pool, org, flagA, "flag_toggled_off", "production")
	seedChangelog(t, pool, org, flagB, "flag_created", "production")
	seedChangelog(t, pool, otherOrg, otherFlag, "flag_created", "production")

	repo, err := NewPostgresSearchRepository(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	t.Run("returns entries across all flags for org", func(t *testing.T) {
		entries, err := repo.GetTimeline(context.Background(), org, Pagination{Page: 1, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3 {
			t.Errorf("expected 3 entries, got %d", len(entries))
		}
	})

	t.Run("org isolation", func(t *testing.T) {
		entries, err := repo.GetTimeline(context.Background(), otherOrg, Pagination{Page: 1, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Errorf("expected 1 entry for other org, got %d", len(entries))
		}
	})

	t.Run("pagination", func(t *testing.T) {
		entries, err := repo.GetTimeline(context.Background(), org, Pagination{Page: 1, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Errorf("expected 2 entries on page 1, got %d", len(entries))
		}
	})
}

func TestPostgresSearch(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	org := seedOrg(t, pool, "org-search")
	seedFlag(t, pool, org, "Checkout Redesign", "launchdarkly", []string{"production"})
	seedFlag(t, pool, org, "New Checkout Flow", "launchdarkly", []string{"production"})
	seedFlag(t, pool, org, "Dark Mode", "launchdarkly", []string{"production"})

	repo, err := NewPostgresSearchRepository(context.Background(), testPGConnString)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	t.Run("returns matching flags", func(t *testing.T) {
		results, err := repo.Search(context.Background(), SearchRequest{OrgID: org, Query: "checkout", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 {
			t.Errorf("expected 2 results for 'checkout', got %d", len(results))
		}
	})

	t.Run("prefix match scores higher than substring match", func(t *testing.T) {
		results, err := repo.Search(context.Background(), SearchRequest{OrgID: org, Query: "Checkout", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) < 2 {
			t.Fatalf("expected at least 2 results, got %d", len(results))
		}
		if results[0].Score <= results[1].Score {
			t.Errorf("expected prefix match to score higher: %v vs %v", results[0].Score, results[1].Score)
		}
	})

	t.Run("empty query returns no results", func(t *testing.T) {
		results, err := repo.Search(context.Background(), SearchRequest{OrgID: org, Query: "", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results for empty query, got %d", len(results))
		}
	})
}
