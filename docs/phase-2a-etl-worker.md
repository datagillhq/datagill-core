# Phase 2A: ETL Worker - Implementation Plan

> **Historical design doc.** Written while building; parts are out of date. The code and [`AGENTS.md`](../AGENTS.md) are the source of truth where they disagree.
> Since this plan, LaunchDarkly transforms run as activities inside `LaunchDarklyExtractWorkflow` (no child transform workflows), and the sync cursor lives in `integrations.last_synced_at`.

## Summary

Build the Temporal-based ETL worker for LaunchDarkly feature flag ingestion. The worker extracts audit logs and flag details from LaunchDarkly, normalizes them to the canonical DataGill schema, and persists to PostgreSQL.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      Temporal Worker                             │
├─────────────────────────────────────────────────────────────────┤
│  Workflows:                                                      │
│  ├── FeatureFlagETLWorkflow (router by source)                  │
│  ├── LaunchDarklyExtractWorkflow (pagination + fan-out)         │
│  ├── LaunchDarklyChangelogTransformWorkflow (fire-and-forget)   │
│  └── LaunchDarklyFeatureFlagTransformWorkflow (fire-and-forget) │
├─────────────────────────────────────────────────────────────────┤
│  Activities:                                                     │
│  ├── ListAuditLogs, GetFlagDetail (extract)                     │
│  ├── NormalizeChangelog, NormalizeFlagDetail (transform)        │
│  └── SaveSourceRecord, SaveFeatureFlag, SaveChangelog (load)    │
└─────────────────────────────────────────────────────────────────┘
         │                                    │
         ▼                                    ▼
   LaunchDarkly API                     PostgreSQL
```

---

## File Structure

```
datagill-core/
├── activities/                    # NEW MODULE
│   ├── go.mod
│   ├── launchdarkly_client.go     # HTTP wrapper with backoff
│   ├── launchdarkly_client_test.go
│   ├── launchdarkly.go            # Extract + normalize activities
│   ├── launchdarkly_test.go
│   └── shared.go                  # Save activities (all providers)
├── workflows/                     # NEW MODULE
│   ├── go.mod
│   ├── etl.go                     # Router workflow
│   ├── launchdarkly_extract.go    # Pagination + Continue-As-New
│   ├── launchdarkly_extract_test.go
│   ├── launchdarkly_transform.go  # Child workflows
│   └── launchdarkly_transform_test.go
├── cmd/worker/
│   ├── go.mod                     # UPDATE - add temporal, prometheus
│   ├── main.go                    # UPDATE - full implementation
│   ├── config.go                  # NEW
│   ├── metrics.go                 # NEW
│   └── integration_test.go        # NEW
└── go.work                        # UPDATE - add activities, workflows
```

---

## Implementation Steps

### Step 1: Create Module Structure

**Files:**
- `activities/go.mod` - new module with temporal SDK, zerolog
- `workflows/go.mod` - new module with temporal SDK
- Update `go.work` to include both modules

**Dependencies:**
- `go.temporal.io/sdk v1.29.1`
- `github.com/rs/zerolog v1.33.0`
- `github.com/prometheus/client_golang v1.19.1`

---

### Step 2: LaunchDarkly HTTP Client (2A.1)

**File:** `activities/launchdarkly_client.go`

```go
type LaunchDarklyClient struct {
    httpClient *http.Client
    apiKey     string
    baseURL    string
}

func (c *LaunchDarklyClient) ListAuditLog(ctx, before time.Time, limit int) ([]LDAuditEntry, error)
func (c *LaunchDarklyClient) GetFlagDetail(ctx, projectKey, flagKey string) (LDFlagDetail, error)
```

**Key behaviors:**
- 10s timeout per request
- Handles 429 with `Retry-After` header (Temporal retries)
- Returns `InvalidAPIKeyError` on 401/403 (non-retryable)
- Returns `NotFoundError` on 404 (skip, don't fail)

---

### Step 3: Shared Save Activities (2A.3)

**File:** `activities/shared.go`

```go
type SharedActivities struct {
    repo repository.Repository
}

func (a *SharedActivities) SaveSourceRecord(ctx, models.SourceRecord) error
func (a *SharedActivities) SaveFeatureFlag(ctx, models.FeatureFlag) error
func (a *SharedActivities) SaveChangelog(ctx, models.FeatureFlagChangelog) error
```

All three call the existing repository methods (idempotent upserts).

---

### Step 4: Extract Activities (2A.2)

**File:** `activities/launchdarkly.go`

```go
type LaunchDarklyActivities struct {
    client *LaunchDarklyClient
    repo   repository.Repository
}

func (a *LaunchDarklyActivities) ListAuditLogs(ctx, input ListAuditLogsInput) (ListAuditLogsOutput, error)
func (a *LaunchDarklyActivities) GetFlagDetail(ctx, input GetFlagDetailInput) (LDFlagDetail, error)
```

Each activity saves raw JSON to `source_records` before returning.

---

### Step 5: Normalize Activities (2A.4)

**File:** `activities/launchdarkly.go` (continued)

```go
func (a *LaunchDarklyActivities) NormalizeChangelog(ctx, input) (FeatureFlagChangelog, error)
func (a *LaunchDarklyActivities) NormalizeFlagDetail(ctx, input) (FeatureFlag, error)
```

**TitleVerb to EventType mapping** (canonical `event_type` column):

| LaunchDarkly TitleVerb | EventType |
|------------------------|-----------|
| `created` | `created` |
| `deleted` | `deleted` |
| `turned targeting on/off` | `toggled` |
| `updated`, `changed`, `added`, `removed` | `updated` |

The raw provider action (e.g. `flag_enabled`, `flag_targeting_on`) is stored in `data.change_type` for finer-grained display.

**Actor extraction:**
- `entry.Member.Email` for user actions → set `actor.email`
- `entry.Token.ServiceToken = true` or `entry.App` present → leave `actor.email` empty (automated action)

---

### Step 6: Workflows (2A.5)

**File:** `workflows/etl.go`
```go
func FeatureFlagETLWorkflow(ctx, input) error
// Routes to provider-specific workflow by integration.Source
```

**File:** `workflows/launchdarkly_extract.go`
```go
func LaunchDarklyExtractWorkflow(ctx, integration) error
// 1. Paginate ListAuditLogs (sequential)
// 2. Deduplicate flag keys
// 3. GetFlagDetail for each (parallel via workflow.Go)
// 4. Fire-and-forget child workflows for transforms
// 5. Continue-As-New after 100 pages
```

**File:** `workflows/launchdarkly_transform.go`
```go
func LaunchDarklyChangelogTransformWorkflow(ctx, input) error
func LaunchDarklyFeatureFlagTransformWorkflow(ctx, input) error
// Each: normalize -> save
```

**Activity Options:**

| Activity Type | Timeout | Retries | Non-Retryable |
|--------------|---------|---------|---------------|
| Extract (API) | 60s | 5 | `InvalidAPIKeyError` |
| Transform (CPU) | 10s | 3 | - |
| Save (DB) | 30s | 5 | - |

---

### Step 7: Worker Registration (2A.6)

**File:** `cmd/worker/main.go`

```go
func main() {
    // 1. Load config from env
    // 2. Initialize zerolog
    // 3. Connect to Postgres (repository)
    // 4. Connect to Temporal
    // 5. Create activities with dependencies
    // 6. Register workflows and activities
    // 7. Start metrics server on :9090
    // 8. Run worker on task queue "datagill-etl"
    // 9. Graceful shutdown on SIGINT/SIGTERM
}
```

**Environment variables:**
- `TEMPORAL_ADDRESS` (default: `localhost:7233`)
- `APP_DATABASE_URL` (required)
- `LD_API_KEY` (required for LD provider)
- `LOG_LEVEL` (default: `info`)
- `METRICS_PORT` (default: `9090`)

---

### Step 8: Integration Test (2A.7)

**File:** `cmd/worker/integration_test.go`

```go
//go:build integration

func TestLaunchDarklyETLIntegration(t *testing.T) {
    // 1. testcontainers: Postgres with migrations
    // 2. temporaltest: Temporal dev server
    // 3. httptest: Mock LD API server
    // 4. Seed integration row
    // 5. Execute FeatureFlagETLWorkflow
    // 6. Assert rows in source_records, feature_flags, changelog
    // 7. Run again -> assert idempotency (no duplicates)
}
```

---

## File Summary

| File | Action |
|------|--------|
| `activities/go.mod` | Create |
| `activities/launchdarkly_client.go` | Create |
| `activities/launchdarkly_client_test.go` | Create |
| `activities/launchdarkly.go` | Create |
| `activities/launchdarkly_test.go` | Create |
| `activities/shared.go` | Create |
| `workflows/go.mod` | Create |
| `workflows/etl.go` | Create |
| `workflows/launchdarkly_extract.go` | Create |
| `workflows/launchdarkly_extract_test.go` | Create |
| `workflows/launchdarkly_transform.go` | Create |
| `workflows/launchdarkly_transform_test.go` | Create |
| `cmd/worker/go.mod` | Update |
| `cmd/worker/main.go` | Update |
| `cmd/worker/config.go` | Create |
| `cmd/worker/metrics.go` | Create |
| `cmd/worker/integration_test.go` | Create |
| `go.work` | Update |

---

## Verification

1. **After modules created:** `go build ./...` passes
2. **After HTTP client:** `go test ./activities/... -run TestClient`
3. **After activities:** `go test ./activities/... -run TestNormalize`
4. **After workflows:** `go test ./workflows/...` with mocked activities
5. **Lint:** `make lint` passes
6. **Integration test:**
   ```bash
   docker run --rm -v $(pwd):/app -v /var/run/docker.sock:/var/run/docker.sock \
     -e TESTCONTAINERS_RYUK_DISABLED=true \
     -e TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal \
     -w /app golang:latest \
     go test -tags=integration ./cmd/worker/...
   ```
7. **E2E smoke test:**
   - `docker compose up -d`
   - Insert integration row with real LD API key
   - Trigger workflow via Temporal UI at `:8080`
   - Verify data in Postgres and metrics in Grafana
