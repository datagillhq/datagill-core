Create a new database migration. The migration purpose is: $ARGUMENTS

Read the Data model section of docs/architecture.md and the existing files in db/migrations/ before writing anything. Every migration decision must be consistent with them.

---

## Naming convention

Migration files live in `db/migrations/`. The filename format is:

```
{NNN}_{snake_case_description}.sql
```

- `NNN` is the next sequential number (check existing files — never reuse a number)
- Description is lowercase snake_case, describes what changes (not why)
- Examples: `002_add_feature_flag_indexes.sql`, `003_add_pgvector_extension.sql`

---

## File structure

Every migration file has two clearly marked sections:

```sql
-- migrate:up
-- (your changes here)


-- migrate:down
-- (rollback SQL here)
```

The `down` section must exactly reverse the `up` section. If a `down` is genuinely unsafe (e.g., dropping a column with data), add a comment explaining why and use `SELECT 1; -- intentionally non-reversible` as a placeholder — never leave it blank.

---

## Rules for every migration

**Schema changes**
- New columns: always `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`
- New tables: always `CREATE TABLE IF NOT EXISTS`
- New indexes: always `CREATE INDEX IF NOT EXISTS` or `CREATE UNIQUE INDEX IF NOT EXISTS`
- Never `DROP COLUMN` or `DROP TABLE` without confirming the column/table is unused across all services and all deployed versions
- Never rename a column directly — add the new column, migrate data, remove the old one in separate migrations

**Type choices (from docs/engineering-standards.md, PostgreSQL)**
- Timestamps: always `timestamptz` — never `timestamp`
- Enums: always `text` with a `CHECK` constraint — never Postgres `ENUM` type
- UUIDs: always `uuid` type with `DEFAULT uuid_generate_v4()` (or deterministic from application)
- Primary keys: `bigserial` for internal PKs, `uuid` for `key` columns (cross-table correlation)

**Index rules**
- Adding an index on a large existing table: use `CREATE INDEX CONCURRENTLY` to avoid locking
- `CONCURRENTLY` cannot run inside a transaction — wrap the file in `-- migrate:no-transaction` if needed
- Run `EXPLAIN (ANALYZE, BUFFERS)` on the target query before and after — document the result in a comment in the migration file
- Never add a GIN index "just in case" — only add it when you have a slow query to prove the need

**Data migrations**
- Data migrations that touch many rows must be batched: `UPDATE ... WHERE id BETWEEN $start AND $end`
- Never run a data migration and a schema change in the same migration file — schema first, data second, in separate files
- Data migrations must be idempotent: running them twice must produce the same result

**Multi-tenant safety**
- Any new column on a multi-tenant table (`feature_flags`, `feature_flag_changelog`, `source_records`, `integrations`) must include `org_id` in its relevant indexes
- Verify that any new query the migration enables will have `org_id` as the first predicate

---

## Steps to create the migration

1. **Determine the next sequence number**
   ```bash
   ls db/migrations/
   ```

2. **Write the migration file** following the rules above

3. **Verify it applies cleanly**
   ```bash
   make migrate
   ```
   Expected: `migrate` reports the new version number with no errors.

4. **Verify it rolls back cleanly**
   ```bash
   # Roll back one version
   migrate -path db/migrations -database $APP_DATABASE_URL down 1
   # Re-apply
   make migrate
   ```
   Both must succeed without errors.

5. **Verify the docs still hold** — confirm the post-migration schema is consistent with the Data model section of docs/architecture.md. If the migration adds a table or changes a convention, update that section in the same PR.

6. **Run integration tests**
   ```bash
   go test -run Integration ./repository/...
   ```
   Integration tests use `testcontainers-go` which runs migrations against a fresh Postgres instance — this is the ground truth test that the migration is correct.

---

## Common migration patterns

**Add a nullable column**
```sql
-- migrate:up
ALTER TABLE feature_flags ADD COLUMN IF NOT EXISTS last_evaluated_at timestamptz;

-- migrate:down
ALTER TABLE feature_flags DROP COLUMN IF EXISTS last_evaluated_at;
```

**Add a non-nullable column with a default**
```sql
-- migrate:up
ALTER TABLE feature_flags ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1;

-- migrate:down
ALTER TABLE feature_flags DROP COLUMN IF EXISTS version;
```

**Add an index on a large table (non-blocking)**
```sql
-- migrate:no-transaction

-- migrate:up
-- EXPLAIN ANALYZE on GET /flags?org_id=X showed seq scan at 500k rows; index reduces to index scan at <5ms
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_feature_flags_integration_id
    ON feature_flags (integration_id);

-- migrate:down
DROP INDEX CONCURRENTLY IF EXISTS idx_feature_flags_integration_id;
```

**Add a GIN index for JSONB query**
```sql
-- migrate:no-transaction

-- migrate:up
-- Required for: data @> '{"environments": {"production": {"enabled": true}}}'
-- Verified with EXPLAIN ANALYZE: seq scan 120ms → index scan 2ms at 50k rows
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_feature_flags_data_environments
    ON feature_flags USING GIN ((data->'environments'));

-- migrate:down
DROP INDEX CONCURRENTLY IF EXISTS idx_feature_flags_data_environments;
```

---

## Definition of done

- [ ] File named correctly with next sequential number
- [ ] Both `up` and `down` sections present and correct
- [ ] `make migrate` succeeds
- [ ] Rolling back and re-applying succeeds
- [ ] Integration tests pass: `go test -run Integration ./repository/...`
- [ ] docs/architecture.md Data model updated if a table or convention changed
- [ ] Large-table indexes use `CONCURRENTLY` and `-- migrate:no-transaction`
