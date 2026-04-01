# Frontend: Integration Connection Modal — Shared Component

## Overview

The integration connection modal is a **shared component** used in two places:
1. **Onboarding step 2** — connecting your first integration
2. **Settings/Integrations page** — connecting new or re-configuring existing integrations

The underlying API calls are identical in both contexts. Only the post-success behavior differs.

---

## Modal States

```
idle          → user has opened modal, API key field empty (create) or masked (edit)
saving        → "Save/Connect" clicked, waiting on POST /v1/integrations or PUT /v1/integrations/{key}
              → validation happens server-side as part of the save
success       → save succeeded, green checkmark shown near API key field, modal closes shortly after
error         → save failed (invalid key or provider unreachable), inline error shown, field still editable
```

There is no separate "validating" state — validation is part of the save request.

---

## Props

```ts
type IntegrationModalProps = {
  mode: 'create' | 'edit'
  provider: Provider                  // metadata: name, logo, description
  existingIntegrationKey?: string     // required when mode === 'edit'
  onSuccess: (integration: Integration) => void
  onCancel: () => void
}
```

---

## Layout (from mock)

```
┌─────────────────────────────────────────┐
│ Configure Integration              [×]  │
│                                         │
│ [Provider logo] Provider Name           │
│ Description text                        │
│                         ┌─────────────┐ │
│ API Key field [••••] ✓  │ Data        │ │
│ [link to docs]          │ Sovereignty │ │
│                         │ AES-256     │ │
│                         │ Read Only   │ │
│                         └─────────────┘ │
│                                         │
│ [Cancel]          [Save/Connect →]      │
└─────────────────────────────────────────┘
```

The green checkmark (✓) appears next to the API key field on successful save, before the modal closes.

---

## Behavior

### Save/Connect button
- Always enabled (user can attempt save at any time)
- Calls `POST /v1/integrations` (create) or `PUT /v1/integrations/{key}` (edit)
- Validation happens server-side as part of the request — no pre-flight validate call
- On success: show green checkmark near the API key field, then close modal and call `onSuccess(integration)`
- On failure: show inline error derived from the error code (see error code mapping below)

### API key field
- In `create` mode: empty
- In `edit` mode: show masked placeholder (`••••••••`), **not** the real key (the API never returns it)
- In `edit` mode: if the user leaves the field untouched, the `PUT` request is sent without an `apiKey` field — the backend keeps the existing key and skips validation
- In `edit` mode: if the user clicks into the field, clear it so they can type a new key — this signals intent to replace
- Never reveal the stored key

### Cancel button
- Calls `onCancel()`, no API calls fired
- If a save is in-flight, cancel the request

---

## Error Code Mapping

The backend returns error codes. The frontend maps them to display strings.

| Code | Display message |
|---|---|
| `INTEGRATION_INVALID_API_KEY` | "Invalid API key. Please check your account settings." |
| `INTEGRATION_PROVIDER_UNREACHABLE` | "Could not reach the provider. Please try again." |
| `INTEGRATION_DUPLICATE_PROVIDER` | "An integration for this provider is already connected." |

Errors are shown inline below the API key field.

---

## Context-Specific Behavior (via `onSuccess`)

The modal itself is context-agnostic. The parent decides what happens after success:

**Onboarding context:**
```ts
onSuccess={(integration) => {
  markIntegrationConnected(integration.provider)
  // enables "Continue to Verification" if at least one connected
}}
```

**Settings context:**
```ts
onSuccess={(integration) => {
  if (mode === 'create') addToIntegrationList(integration)
  if (mode === 'edit')   updateIntegrationInList(integration)
}}
```

---

## Provider Metadata

The modal receives a `Provider` object from the static frontend catalog. The backend's `GET /v1/integrations/providers` response is cross-referenced to know which providers are implemented vs. "Coming Soon".

```ts
type Provider = {
  id: string              // 'launchdarkly', 'salesforce', etc.
  name: string            // 'LaunchDarkly'
  logo: string            // asset path
  description: string
  docsLink: string        // shown as "Launch Account Settings → Access Tokens"
  tags: string[]          // ['DEVOPS', 'RECOMMENDED']
}
```

---

## Error States

| Scenario | Display |
|---|---|
| Invalid API key | Inline error below field, using `INTEGRATION_INVALID_API_KEY` mapping |
| Provider unreachable | Inline error using `INTEGRATION_PROVIDER_UNREACHABLE` mapping |
| Duplicate provider | Inline error using `INTEGRATION_DUPLICATE_PROVIDER` mapping |
| Network offline | "Unable to reach server. Check your connection." |
