# S316: People: add people and grant apps from Homestead

## Goal
Implements ADR-043 §4–6. Homestead is the primary way to maintain users.
From the dashboard or the CLI, the owner adds the people in the household,
maintains them through their whole life, and grants each one the apps they
use, as user or admin. Authentik only stores them and does the sign-in, so
the owner never needs its admin UI for people.

Depends on S314.

## Scope
- **Store:**
  - `people`: id, display name, email, Authentik user pk, state
    (active/disabled), created_by, created_at.
  - `people_grants`: person, app, role (user/admin).
- **API:**
  - list, add, edit (name, username, email), password reset (recovery
    link or a generated password revealed once), second factor
    reset, disable/enable, remove, grant, revoke, and adopt an existing
    Authentik user;
  - a one-time enrollment link, or a generated password revealed once;
  - owner session plus confirmation;
  - step-up for admin grants, password resets and removal;
  - every action audited, with no secrets in the audit.
- **Token:**
  - The People feature requires the kept Authentik token (the ADR-033
    opt-in, sealed).
  - Without it, the page explains why and offers to keep one, stating what
    the token can do.
- **Dashboard People page** (Apps or System nav):
  - the people list, with each person's apps and roles as lamps;
  - add-person and grant dialogs;
  - the owner shown as always having every app.
  - It follows lamplight, with a masthead and fields, and covers the
    lamplight spec.
- **CLI:**
  - `homesteadctl people list|add|edit|reset-password|reset-mfa|disable|enable|remove|adopt`
  - `homesteadctl people grant|revoke <person> <app> [--admin]`
- **Drift (Homestead's record wins):**
  - Read back the managed people and the `homestead-` group memberships.
  - Changes made in Authentik show as drift, with the recommended action
    **Put back** or the alternative **Keep**.
  - A person deleted in Authentik shows as missing, with **Recreate** or
    **Remove from Homestead**.
  - Authentik users that Homestead did not create are listed as "not
    managed by Homestead", with **Adopt**.
  - Service and system accounts are never listed.
- **Installing an app:** the Sign-in step can grant people in the same
  install, as part of the approved plan.
- **Removing a person:** disables and removes the Authentik user that
  Homestead created. App data stays and the page says so.

## Rules
- AGENTS.md slice rules apply.
- Update PARITY (the parity test enforces it) and DEFERRED.

## Acceptance Criteria
- Go tests cover the store, the routes (access class, step-up and audit),
  the Authentik calls against a fake, drift, and the missing-token path.
- CLI tests cover each command.
- Dashboard specs cover the page, the dialogs, the missing token, drift
  and the install-wizard grants.
- Go, Python and dashboard tests pass; build and embed succeed; PARITY is
  updated.
- Screenshots of People in dark, desktop and 375px.
- A live check on VM 2:
  - add a person, grant them Nextcloud, and they can sign in;
  - reset their password from Homestead, and the new password works;
  - edit their email in Authentik's UI, and Homestead shows drift that
    Put back restores;
  - revoke Nextcloud, and they are refused;
  - grant admin, and they are a Nextcloud admin.
