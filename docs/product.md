# Product

Product goals, requirements, roadmap and open questions. For what is built today, see the
[README](../README.md#what-works-today); for how it is built, see [architecture.md](./architecture.md).

## North Star

DataGill is a **business intelligence platform for engineering teams**. It connects to the tools engineers use to ship software, normalizes the data into a unified model, and surfaces it through a web UI so teams can answer hard questions about their work.

| Data Source | Questions Unlocked |
|-------------|-------------------|
| Feature flags (LaunchDarkly, Unleash, Flagsmith) | What changed before the incident? How long to ship a flag? |
| Code (GitHub) | Which PR shipped this flag? Time from commit → flag on in prod? |
| Incidents (PagerDuty) | Was there an incident open when this flag was rolled back? |
| Business metrics (Datadog) | Did this flag change correlate with a latency spike? |
| Data warehouse (Snowflake) | What did conversion rate look like when this feature launched? |

**MVP = feature flag providers only.** Architecture must accommodate future data source categories without rearchitecting the pipeline.

**Implemented today:** LaunchDarkly (flags) and GitHub (merged PRs, with diffs scanned for flag keys). Unleash and Flagsmith are declared as `models.Source` values but have no extract workflow. The requirement tables below are the product target, not a statement of what is built — `README.md` lists what works today.

## Product Requirements

"All 3" = LaunchDarkly, Unleash, Flagsmith. "LD" = LaunchDarkly only.

### MVP — Lights On

| Feature | Source | Notes |
|---------|--------|-------|
| Activity feed (who changed what, when) | All 3 | LD titleVerb is cleanest canonical description |
| Flag status (off / on / partial) | All 3 | LD direct; Unleash/Flagsmith need inference |
| Trend line (changes over time) | All 3 | Aggregate by day |
| Actor attribution (last modified by) | All 3 | Normalize to actor_email |
| Search by flag name / time range | All 3 | LD supports full-text; others: time range only |

### P0 — Root Cause Analysis

| Feature | Source | Notes |
|---------|--------|-------|
| "What changed in the last X hours?" scoped to prod | All 3 | Filter by environment + time window |
| Automated vs. human change detection | LD | token.serviceToken field distinguishes API vs. human |
| Rollback detection | All 3 | Flag toggled back within short window |
| Per-flag timeline view | All 3 | Paginated history per flag, newest first |

### P1 — Developer Velocity

| Feature | Source | Notes |
|---------|--------|-------|
| Time from flag creation → on in prod | All 3 | Requires first_seen_at per flag |
| Stale flag list (created, never launched) | All 3 | No "enabled" event past X days |
| Dead flag list (launched 60+ days, no changes) | All 3 | Compare last_event_at |
| Flag cleanup suggestions | Derived | Combine stale + dead signals |
| Owner-level velocity | All 3 | Group by actor across flag history |

### P1 — Monthly Snapshot

| Feature | Source | Notes |
|---------|--------|-------|
| Flags launched this month | All 3 | Count enable/100% events |
| Flags cleaned up (deleted) | All 3 | Count delete events |
| Monthly digest | Derived | AI-generated narrative is a P2 enhancement |

### P2 — Intelligence

| Feature | Notes |
|---------|-------|
| Anomaly: high change frequency | Flag changed >N times in 24h |
| Natural language search | LLM over stored embeddings — do not rely on source q param alone |
| Risk score | Blast radius × recent change rate |
| AI narrative summaries per flag | LLM over flag event history |

## Roadmap

Do not build these yet. Do not design the MVP in a way that makes them hard to add.

### Future Data Sources

GitHub (merged PRs + flag references in diffs) is implemented; see [architecture.md](./architecture.md).

| Source | Category | Questions Unlocked |
|--------|----------|-------------------|
| PagerDuty | Incidents | Was there an incident open when this flag was rolled back? |
| Datadog | Metrics | Did error rate spike when this flag was enabled? |
| Snowflake | Warehouse | What was conversion rate during this flag's rollout? |
| Jira / Linear | Project | Which ticket drove this flag? |

Future capabilities: natural language querying, Slack/email alerting, public API, cross-source correlation, VP digest.

## Open Questions

Do not make implementation decisions that depend on unresolved questions.

| Question | Options / Direction | Needed by |
|----------|---------------------|-----------|
| Statsig / PostHog support | Add to source enum? (roadmap, not implemented) | Before next flag provider |
| Backfill concurrency cap | Max concurrent transform workflows during initial 30-day backfill — value TBD | Before backfill impl |
| Weekly digest | Scheduled Temporal workflow vs. separate service | Phase 2 |
| Hosting / compute | Railway, Fly.io, GCP Cloud Run, VPS | Cost analysis |
| Frontend data fetching | TanStack Query, SWR, RSC-only | LLD session |
| PublishEvent downstream | Webhook, Slack, internal bus | Post-MVP |
| feature_flags.data extensions | When to promote type, tags, targeting, variations from post-MVP to typed fields | When P1 features need them |
