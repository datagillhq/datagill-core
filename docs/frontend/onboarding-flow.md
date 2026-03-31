# Frontend: Sign Up + Onboarding Flow — Design Context

## Overview

This document covers the sign-up screen and the 5-step onboarding wizard that follows first sign-in. The goal is to get a new user from zero to a working org with at least one integration connected.

The backend counterpart is `docs/backend/integration-management-api.md`.
The shared integration modal is documented in `docs/frontend/integration-connection-modal.md`.

---

## Ownership Summary

| Step | Screen | Owner |
|---|---|---|
| — | Sign Up | BetterAuth entirely |
| 1 | Create Organization | BetterAuth entirely |
| 2 | Connect integrations | Frontend catalog + DataGill API |
| 3 | Verification (confirmation of connected integrations) | DataGill API (read-only) |
| 4 | Invite team | BetterAuth entirely |
| 5 | Billing | Out of scope |

DataGill's backend is only involved in steps 2–3.

---

## Sign Up

Standard BetterAuth screen. Fields: Full Name, Email Address, Password. SSO options: Google, GitHub.

**DataGill backend:** no involvement. BetterAuth creates the user and session.

**After sign up:** redirect to onboarding step 1.

---

## Onboarding Step 1 — Create Organization

Fields:
- Organization Name (display name)
- Workspace Slug — auto-generated from org name, editable, shown as `datagill.io/[slug]`

**DataGill backend:** no involvement. BetterAuth creates the org and returns `external_org_id`.

**After success:** redirect to step 2. DataGill creates its own org record lazily when the first integration is saved in step 2 — no separate DataGill call needed here.

**Atomicity note:** Investigate whether BetterAuth exposes after-hooks on org creation before implementation. The lazy creation pattern means DataGill's org record and first integration are created together in one DB transaction, so partial failure in DataGill is clean. The edge case is BetterAuth-side failure mid-wizard — understand BetterAuth's recovery behavior before finalizing the onboarding error states.

---

## Onboarding Step 2 — Connect Your First Tool

The user selects and configures integrations. "Skip for now" is available — connecting is recommended but not enforced.

### Provider Grid
- Full catalog with metadata lives statically in the frontend
- `GET /v1/integrations/providers` returns supported provider IDs — frontend cross-references to show "Coming Soon" on unsupported ones
- Search field and Categories filter operate on the local static catalog — no API call

### Selection and Configuration Flow
```
User clicks a provider card
  → card gets a selected state
  → integration connection modal opens (mode: 'create')
  → user enters API key, clicks Save/Connect
  → on success: green checkmark appears near API key field, modal closes
                card shows connected state
  → "X tool(s) selected for connection" count updates in footer
```

Multiple integrations can be configured in sequence. Each opens and closes the modal independently.

### Footer
- "X tool selected for connection" — count of successfully configured integrations
- "Skip for now" — advances to step 4 (team invite)
- "Continue to Verification" — enabled once ≥1 integration successfully configured

---

## Onboarding Step 3 — Verification

Confirmation screen showing connected integrations and their status. No additional API calls — data is already in local state from step 2.

---

## Onboarding Step 4 — Invite Your Team

Fields:
- Multi-email input (tag-style, accepts multiple emails)
- "Send Invites" button → BetterAuth sends invitation emails
- "Already invited" list showing pending members with role badges (Admin, Editor, Viewer)

**DataGill backend:** no involvement. BetterAuth handles invitations and roles.

**Invitation email:** BetterAuth template. Link expires in 7 days. Recipient clicks "Accept Invitation" → joins the org → lands on the dashboard.

---

## Flow

**Sign Up**
1. Browser sends POST /auth/sign-up to BetterAuth
2. BetterAuth returns session + user

**Step 1 — Create Org**
3. Browser sends POST /api/orgs to BetterAuth with org name + slug
4. BetterAuth returns externalOrgId

**Step 2 — Connect Integration**
5. [t=0ms] User selects LaunchDarkly, opens modal, enters API key, clicks Save/Connect
6. [t=0ms] Browser sends POST /v1/integrations to DataGill API
   - DataGill validates the API key internally against the provider
   - DataGill creates the org record + integration in one DB transaction
7. [t=~200ms] On success: DataGill returns 201 — green checkmark shown near API key field, modal closes
   On failure: DataGill returns 400 with error code — inline error shown, user corrects and retries
8. ETL worker picks up the new integration automatically via its polling loop

**Step 4 — Invite Team**
9. Browser sends POST /auth/invite to BetterAuth with teammate emails
10. BetterAuth sends invitation emails (links expire in 7 days)

**Finish**
11. Browser redirects to Dashboard

---

## UX Notes

- The onboarding sidebar (Organization, Integrations, Verification, Team Members, Billing) is always visible showing progress
- Completed steps get a checkmark; current step is highlighted
- Steps are not freely navigable — user moves forward linearly (with Back available)
- The integration modal is an overlay on step 2
- Once BetterAuth org is created (step 1), the user is in the system and can exit the wizard at any point — they go straight to the dashboard and complete integration setup later via Settings/Integrations. No wizard progress state needs to be tracked or restored.
