# Backend: Integration Management — API Design Context

## Overview

This document covers the API design for integration management: the endpoints used during onboarding (connect your first tool) and on the `/settings/integrations` page (manage connected integrations).

All endpoints are versioned under `/v1/`. The API is designed to serve third-party developers as first-class consumers — the DataGill frontend is just one of them.

Read alongside the frontend docs before creating an implementation plan:
- `docs/frontend/onboarding-flow.md`
- `docs/frontend/settings-integrations-page.md`
- `docs/frontend/integration-connection-modal.md`

---

## Authentication & Trust Model

DataGill validates JWTs directly in its own middleware — no API gateway is needed. BetterAuth is a TypeScript library with no Go client, but JWT validation is pure crypto and handled by standard Go libraries (`golang-jwt/jwt` or `lestrrat-go/jwx`).

**JWT flow:**
- BetterAuth issues signed JWTs via its JWT plugin. The `organizationId` claim reflects the user's `activeOrganizationId` — BetterAuth only sets this if the user is a verified member of that org.
- On startup, DataGill fetches BetterAuth's JWKS endpoint (`/.well-known/jwks.json`) to retrieve the public key and caches it.
- A JWT middleware sits early in the middleware stack. On each request it: extracts the `Authorization: Bearer <token>` header, validates the JWT signature against the cached public key, extracts `organizationId` and `userId` claims, and injects both into the request context.
- Requests with a missing, expired, or tampered token are rejected with `401` before reaching any handler.
- JWTs should be **short-lived (15–30 minutes)** since org membership changes won't be reflected until the token expires.

This replaces the existing `X-Org-Id` header middleware — same position in the middleware stack, the org ID now comes from validated JWT claims instead of a trusted header.

This model prevents two classes of attacks:
- **Bogus org ID** — a tampered JWT fails signature validation in middleware
- **Someone else's org ID** — BetterAuth only issues `organizationId` claims for orgs the user is a verified member of

**Action item before implementation:** Verify that BetterAuth's JWT plugin includes `activeOrganizationId` as a claim in the JWT payload. If it requires explicit configuration, document the setup.

---

## Ownership Boundary

**BetterAuth owns:** user creation, sessions, org creation (name + slug), team invitations, org membership and roles.

**DataGill owns:** everything integration-related. Nothing else.

DataGill does not have an explicit org creation endpoint — see lazy org creation below.

---

## Org Record: Lazy Creation

DataGill has an `organizations` table with an `external_org_id` column referencing BetterAuth's org. There is **no explicit org creation endpoint**.

When the first `POST /v1/integrations` request arrives for an org, DataGill extracts `external_org_id` from the JWT claims and creates the org record + the integration together in **one DB transaction**. If that transaction fails, both fail — the user retries and it works cleanly.

### BetterAuth Atomicity

One edge case to investigate before implementation: if BetterAuth successfully creates an org but the onboarding flow is interrupted before the user reaches the integration step, DataGill will have no org record yet. This is acceptable — the user can re-enter the flow and DataGill will create the record on first integration save.

The reverse (DataGill has a record, BetterAuth does not) cannot happen given the lazy creation pattern.

**Action item:** Investigate whether BetterAuth exposes after-hooks or callbacks on org creation that could be used to handle any edge cases in the onboarding wizard state.

---

## Error Format

The backend returns error **codes**, never human-readable messages. The frontend owns the mapping from code to display string. This keeps messaging under frontend control and supports future i18n.

```json
{ "code": "INTEGRATION_INVALID_API_KEY" }
```

Example error codes:
- `INTEGRATION_INVALID_API_KEY` — API key rejected by the provider during validation
- `INTEGRATION_PROVIDER_UNREACHABLE` — provider API timed out or returned 5xx
- `INTEGRATION_DUPLICATE_PROVIDER` — org already has an active integration for this provider
- `INTEGRATION_NOT_FOUND` — integration key does not exist for this org

---

## Endpoints

All endpoints require `Authorization: Bearer <JWT>`; the org comes from the token's `organizationId` claim. All use JSON.

---

### `GET /v1/integrations/providers`

Returns the list of provider IDs that DataGill currently supports. The frontend holds the full catalog with metadata (name, logo, description, tags) — this endpoint tells it which ones are active vs. "Coming Soon".

**Response:**
```json
{
  "providers": ["launchdarkly"]
}
```

As new providers are implemented, they are added to this list. No frontend deploy required.

---

### `POST /v1/integrations`

Create a new integration. **Validation happens internally** — DataGill calls the provider API with the supplied key as part of the save. If the key is invalid, the save fails with an error code. There is no separate validate endpoint.

`pollIntervalSeconds`, `backfillDays`, and `projectKey` are **not accepted from the client**. DataGill owns poll interval and backfill configuration internally (potentially tier-gated). Integrations are project-agnostic — project context lives at the feature flag level, not the integration level.

**Request:**
```json
{
  "provider": "launchdarkly",
  "apiKey": "api-xxxxxxxxxxxx"
}
```

**Response:** `201 Created`
```json
{
  "key": "uuid",
  "provider": "launchdarkly",
  "state": "active",
  "createdAt": "2024-05-12T10:00:00Z"
}
```

**On validation failure:** `400 Bad Request`
```json
{ "code": "INTEGRATION_INVALID_API_KEY" }
```

**Side effects:**
- Creates org record lazily if it doesn't exist (see above), in the same DB transaction
- API key is encrypted at rest (AES-256-GCM) before storage
- ETL worker picks up the new integration automatically via its polling loop
- API key is **never returned** in any response after this point

---

### `GET /v1/integrations`

List all integrations for the org (state != deleted).

**Response:**
```json
{
  "orgId": "...",
  "integrations": [
    {
      "key": "uuid",
      "provider": "launchdarkly",
      "state": "active",
      "createdAt": "2024-05-12T10:00:00Z"
    }
  ]
}
```

API key is **never included** in this response.

---

### `PUT /v1/integrations/{key}`

Update an integration. Accepts a new API key and/or a state change (`active` or `paused`) in the same request. If a new API key is provided, it is validated against the provider before saving.

**Request:**
```json
{
  "apiKey": "api-new-key",
  "state": "paused"
}
```

All fields are optional — send only what is changing. State accepts `active` or `paused`.

**Validation behavior:** validation only runs if `apiKey` is present in the request body. If the user leaves the masked key field untouched in the UI, the frontend omits `apiKey` from the request entirely and the backend keeps the existing key without any provider call.

**Response:** `200 OK` with updated integration object (same shape as GET, no API key).

**On validation failure (if apiKey provided):** `400 Bad Request`
```json
{ "code": "INTEGRATION_INVALID_API_KEY" }
```

---

### `DELETE /v1/integrations/{key}`

Soft-delete an integration. Sets `state = deleted`. The row is retained for audit purposes. The ETL worker stops polling deleted integrations.

**Response:** `200 OK`
```json
{ "key": "uuid", "state": "deleted" }
```

---

## API Key Security

- Keys are encrypted with AES-256-GCM before storage
- Keys are **never returned** by any endpoint after creation
- On the settings page, the modal shows a masked placeholder (`••••••••`) — not the real key
- To update a key, the user must re-enter it in full — there is no "reveal" option
- The key is sent to the backend only once (on save), where it is validated and stored encrypted

---

## Implementation Order Recommendation

1. `GET /v1/integrations/providers` — no DB dependency, unblocks frontend catalog integration
2. `POST /v1/integrations` + lazy org creation — core creation path with internal validation
3. `GET /v1/integrations` — read path for the settings page
4. `PUT /v1/integrations/{key}` — update path (state + key changes)
5. `DELETE /v1/integrations/{key}` — soft delete
