# S314: App access through Authentik groups, closed by default

## Goal
Implements ADR-043 §1–3 and §6 (read-back only). After S314, an app that
signs in with Authentik admits only the owner and the members of its own
groups. Its admins get the app's admin role from a group.

Depends on ADR-043 being accepted, and on S312 and S313.

## Scope
- **Groups:**
  - The dashboard sign-in plan creates `homestead-owner` and puts the
    linked owner user in it, matched by the `(issuer, sub)` pair (the pk
    found through the user's `uid` or `sub`).
  - The app sign-in plan creates `homestead-<app>-users` and
    `homestead-<app>-admins`.
  - The owner is in both groups.
  - Each plan lists every group, membership and binding it creates or
    changes.
- **Bindings:**
  - Each app application is bound to its two groups plus `homestead-owner`.
  - The dashboard application is bound to `homestead-owner`.
  - The ADR-033 `(issuer, sub)` check is unchanged.
- **Groups claim:**
  - Each app provider gets a scope mapping that emits `groups` filtered to
    the `homestead-` prefix.
  - The adapters map admins:
    - Nextcloud: the user_oidc group provisioning, with admin set from
      `homestead-nextcloud-admins`.
    - Jellyfin: `AdminRoles` and `Roles` in the SSO plugin XML.
    - Immich: its OAuth role claim.
  - An app whose admin cannot be mapped says so in its plan.
- **Existing sign-ins:**
  - Read back the bindings.
  - When an app has none, the Sign-in page and the app's Manage panel warn
    "open to every Authentik user" and offer to re-plan.
  - Nothing is applied without approval.
- **Uninstall:** the uninstall plan lists the app's two groups and removes
  them.
- **Install wizard:** the Sign-in step and the review state who can sign
  in: "you, and people you grant on the People page (coming next)". Until
  S315, granting is done by adding a person to the group in Authentik, and
  the done step says so.
- **Read-back:** report the groups, members and bindings Homestead created.
  A difference shows as drift and is not reverted.

## Rules
- AGENTS.md slice rules apply.
- Use the lamplight design rules.
- Update PARITY for any route or CLI change.
- Never touch groups without the `homestead-` prefix, or users other than
  the linked owner.

## Acceptance Criteria
- Go tests cover:
  - plan objects and digest for groups, bindings and the claim mapping;
  - idempotent apply;
  - an existing sign-in with no bindings is warned and not changed;
  - the adapters' admin mapping for each app;
  - uninstall removes the app's groups;
  - groups without the prefix are never touched.
- Dashboard specs cover the warning and the wizard wording.
- Go, Python and dashboard tests pass; build and embed succeed.
- A live check on VM 2:
  - a second Authentik user with no group is refused by Nextcloud;
  - after being added to `homestead-nextcloud-users`, they can sign in;
  - in `-admins`, they are a Nextcloud admin.
