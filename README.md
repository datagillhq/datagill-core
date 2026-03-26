```
      /`·.¸
     /¸...¸`:·
 ¸.·´  ¸   `·.¸.·´)
: © ):´;      ¸  {
 `·.¸ `·  ¸.·´\`·¸)
     `\\´´\¸.·´

    d a t a g i l l
```

**Business intelligence for Saas companies.**

---

DataGill connects to the tools your team uses to ship software and gives you a single place to answer the questions that matter most.

### The Problem

When something breaks in production, you're jumping between tabs — your feature flag dashboard, your metrics tool, your deployment logs, your incident tracker. The answer to "what changed?" is spread across five different systems, none of which talk to each other.

When leadership asks "how fast are we actually shipping?", you're pulling CSVs and doing the math yourself.

When it's time to clean up stale flags or deprecated experiments, nobody knows which ones are safe to remove.

### What DataGill Does

DataGill ingests data from the tools you already use, normalizes it into a unified timeline, and lets you query across all of it in one place.

```
Feature Flags  ──┐
Business Metrics ─┤                    ┌─ Activity Feed
Data Warehouse  ──┤──▶  DataGill  ──▶  ├─ Root Cause Analysis
Source Control  ──┤                    ├─ Velocity Metrics
    ...         ──┘                    └─ Company Progress
```

**MVP:** Feature flag providers (LaunchDarkly, PostHog, Statsig).
**Roadmap:** Datadog, Snowflake, GitHub, and more.

### The Questions We Answer

| Question | Today |
|----------|-------|
| What changed in production in the last 2 hours? | ✅ |
| Was that change a human or an automated process? | ✅ |
| How long does it take us to ship a feature flag to production? | ✅ |
| Which flags are safe to clean up? | ✅ |
| How many flags did we launch this month, and who drove them? | ✅ |
| Did that flag change correlate with a metric anomaly? | Roadmap |
| Which commit introduced this flag? Time from PR → prod? | Roadmap |

---

### Local Dev Quick Start

Backend:

```bash
cd datagill-core
cp .env.example .env
make docker-up
docker compose up search --build
```

Notes:

- The search service runs on `http://localhost:8082`.
- Grafana from `datagill-core` Docker Compose runs on `http://localhost:3000`.
- The DataGill UI runs on `http://localhost:3001`; see `../datagill-ui/README.md` for frontend setup and product UI docs.
- If you want to run the search service with `go run`, you still need `.env`
  loaded because `APP_DATABASE_URL` is required.

---

> See [`AGENTS.md`](./AGENTS.md) for the full architecture, product requirements, and open questions.
