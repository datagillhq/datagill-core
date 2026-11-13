# Contributing

Thanks for your interest! DataGill is a small, early-stage project, so please open an issue
to discuss anything bigger than a small fix before you start.

## Before you code

Read [`AGENTS.md`](./AGENTS.md). It lists the rules every change must follow (tenant
isolation, idempotent writes, Temporal, logging, SQL) and links to the longer docs in `docs/`.
If your change alters the architecture, update [`docs/architecture.md`](./docs/architecture.md)
in the same PR and explain why.

## Setup

See the quick start in [`README.md`](./README.md) and the full guide in
[`LOCALSETUP.md`](./LOCALSETUP.md). You'll need Go (version in `go.work`), Docker, and
[golangci-lint v1.64.8](https://golangci-lint.run/) for linting.

## Checks

Run these before opening a PR. CI runs the same targets on every pull request.

```bash
make test-unit   # unit tests, no external dependencies
make lint        # golangci-lint, all modules
make build       # worker + search binaries
make vuln        # govulncheck, per module (go install golang.org/x/vuln/cmd/govulncheck@latest)
make test-int    # integration tests against real Postgres; needs Docker
```

New code needs tests. Workflows are tested with Temporal's test environment, provider
clients with `httptest` servers, and the repository layer with testcontainers
(`-tags=integration`).

## Pull requests

1. Branch from `main`. Never push to `main` directly.
2. Keep each PR focused on one change. Separate refactors from behavior changes.
3. Write commit messages in the existing style: `type(scope): summary`, for example
   `fix(github): retry 403 rate limits`.
4. Explain in the PR description what changed and why, and how you tested it.
5. CI (lint, test, build, vulnerability scan) must pass, and a maintainer must approve
   before merge.

## Database migrations

Migrations are forward-only. Add a new numbered file in `db/migrations/`; never edit one
that has already been merged.

## Security

Don't report vulnerabilities in issues. See [`SECURITY.md`](./SECURITY.md).
