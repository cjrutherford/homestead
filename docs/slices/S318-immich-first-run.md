# S318 — Immich sets itself up

## Goal
From the owner's VM 2 acceptance run (2026-10-27): after installing Immich
with sign-in through Authentik, opening Immich still asked the owner to
create an account. Immich reported `isInitialized: false`. Until an
administrator exists, its web app shows "create an administrator" to
everyone, sign-in button or not. Nextcloud sets itself up from its
environment (S312); Immich has no such setting.

## Scope
- A manifest may name a **first-run routine** (`firstrun:`): first-party
  code in `internal/firstrun`, the service it talks to, the administrator
  account and a declared secret for its password. That secret is held by
  Homestead: it has no consumer service, and must be `immutable` and
  `reveal` (S312 reveal rules: step-up, audited, never in argv or logs).
- Immich's routine runs `node` in the server container (the image's own
  runtime) against Immich's API on 127.0.0.1:2283, with the account and
  password on stdin. If Immich has no administrator, it creates
  `admin@homestead.lan` with the generated password
  (`POST /api/auth/admin-sign-up`), signs in as it and finishes the
  administrator onboarding (`POST /api/system-metadata/admin-onboarding`).
  So the web app opens on its login page with **Sign in with Authentik**.
  An Immich already set up is left alone. If its administrator is the
  owner's own (the login fails), Homestead does nothing.
- It runs as the last step of install (`first-run`), waiting, bounded
  (5 minutes), for Immich to answer. If it fails, the app stays installed
  and the error says so. It also runs before sign-in changes anything
  (`app-first-run`), so **Apply sign-in again** retries it, and existing
  installs (VM 2) get their administrator when sign-in is next applied.
- The password shows in Manage → Secrets with **Reveal**, marked "kept by
  Homestead". CLI: `homesteadctl apps secrets reveal immich admin_password`.

## Rules
AGENTS.md slice rules apply. No route, command or surface is added, so
PARITY is unchanged. No ADR: the curated manifests already carry first-party
adapter code by name (ADR-039 §3), and this follows that pattern.

## Acceptance Criteria
- Go tests: the routine waits, creates, keeps the password, reports a
  refusal, gives up bounded and never puts the password in argv. Manifest
  validation refuses an unknown routine, service or secret, a missing
  account, and a held secret that is rotatable or not revealable; a
  consumerless secret without a first run is still refused. Sign-in runs
  the first run before anything else changes.
- Live on VM 2: Immich reports `isInitialized: true` and `isOnboarded: true`,
  its login page offers Authentik, and the revealed password signs in as
  the administrator.
