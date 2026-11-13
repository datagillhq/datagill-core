# Integrations API

Endpoints the UI uses to connect and manage provider integrations. All JSON. Every endpoint
except the GitHub callback requires `Authorization: Bearer <JWT>`; the org comes from the
token's `organizationId` claim. Errors are `{"code": "..."}`; the UI maps codes to messages.

Integration object (returned by create, list, update, delete; the API key is never returned):

```json
{ "key": "uuid", "source": "launchdarkly", "state": "active",
  "config": { "installation_id": 123, "account_login": "acme",
              "account_type": "Organization", "repository_selection": "all" } }
```

`config` fields are filled for GitHub integrations and empty for LaunchDarkly.

## `GET /v1/integrations/providers`

Providers that can be connected: `{"providers": ["launchdarkly", "github"]}`.

## `POST /v1/integrations`

Connect an API-key provider (LaunchDarkly). The key is validated against the provider before
saving, encrypted with AES-256-GCM, and never returned. On success the org row is created if
needed, and the Temporal schedules are created, with a first sync triggered immediately.
If the schedules cannot be created, the new integration is soft-deleted and the call returns
`500`, so the client can simply retry.

```json
{ "source": "launchdarkly", "api_key": "api-xxxxxxxx" }
```

`201` with the integration object. Errors:

| Status | Code |
|--------|------|
| 400 | `INVALID_REQUEST_BODY`, `INTEGRATION_INVALID_API_KEY`, `INTEGRATION_PROVIDER_UNREACHABLE`, `INTEGRATION_USE_GITHUB_AUTH` (GitHub must use the flow below) |
| 409 | `INTEGRATION_DUPLICATE_PROVIDER` (the org already has an active or paused integration for this source) |

## `GET /v1/integrations`

`{"integrations": [ ...integration objects... ]}`. Active and paused only; deleted ones are hidden.

## `PUT /v1/integrations/{key}`

Partial update. Send only what changes:

```json
{ "api_key": "api-new-key", "state": "paused" }
```

`state` is `active` or `paused`. A new `api_key` is validated first; if it is omitted the stored
key is kept with no provider call. `200` with the integration object. Errors: `400`
`INVALID_INTEGRATION_KEY`, `INVALID_REQUEST_BODY`, `INTEGRATION_INVALID_API_KEY`,
`INTEGRATION_PROVIDER_UNREACHABLE`; `404` `INTEGRATION_NOT_FOUND`.

Pausing does not delete the Temporal schedule: each scheduled run checks the state and exits
without calling the provider.

## `DELETE /v1/integrations/{key}`

Soft delete: sets `state = deleted` and keeps the row. `200` with the integration object.
`404` `INTEGRATION_NOT_FOUND`. Later scheduled runs exit without calling the provider.

## GitHub connection

GitHub uses a GitHub App install instead of an API key. See
[architecture.md](../architecture.md) for why the flow works this way.

1. `POST /v1/auth/github` with optional `{"return_to": "/settings/integrations"}` (must be a
   relative path). Returns `{"install_url": "...", "expires_at": "..."}`.
2. The UI redirects the browser to `install_url`; the user installs and authorizes the app.
3. GitHub calls `GET /v1/auth/github` (no JWT; a signed `state` ties it to the org). The service
   verifies the user can see the installation, creates the integration and its schedules, then
   redirects to `FRONTEND_URL` + `return_to` with `?provider=github&status=connected`, or
   `status=error&code=<invalid_state|github_auth_failed|integration_create_failed>`.
