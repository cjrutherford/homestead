# People and App Access Through Authentik Groups

## Status
Accepted

Accepted by the owner on 2026-10-26. Owner decisions:
- An app is closed by default: only the owner and people granted to it can
  sign in.
- People are added and managed in Homestead (a People page and
  `homesteadctl people`), not only in Authentik's admin interface.
- Authentik groups grant admin rights inside apps through per-app admin
  groups.
- Homestead is the primary way to maintain users. Authentik's admin UI is
  for break-glass only (§4, §6).

Amends ADR-033 (dashboard sign-in) and ADR-040 (app sign-in). ADR-033's
rule that Homestead never touches Authentik users, groups, policies and
bindings is narrowed to the objects named here. Its rule that the
dashboard is owner-only, and that no Authentik group or claim ever grants
Homestead administration, is unchanged.

## Context
App sign-in (ADR-040) creates one OIDC provider and one application per app
and binds no policy. Authentik lets every user in to an application that has
no bindings. So everyone the owner adds to Authentik can sign in to every
app, and apps are now published through the tunnel (ADR-041, S313). Roles
inside the apps are not mapped either: each app's admin is its own local
account, and Jellyfin makes every first sign-in an ordinary user. The owner
wants access set up automatically from the owner and the other people
in the household, with groups in Authentik.

## Decision
### 1. Groups Homestead owns
Homestead creates and owns Authentik groups with a reserved prefix:
- `homestead-owner`: the owner's linked Authentik user (the ADR-033
  `(issuer, sub)` pair), and nobody else. Homestead keeps the membership
  equal to the link.
- `homestead-<app>-users` and `homestead-<app>-admins` for each installed
  app that uses Authentik sign-in. An admin is also a user.

Homestead never edits or deletes groups without the prefix, users it did not
create, flows, stages, or Authentik's own settings. The groups are named in
plans and in the "will touch" list. Uninstalling an app removes its two
groups after the uninstall plan lists them.

### 2. Closed by default
Each app's Authentik application gets group bindings:
`homestead-<app>-users`, `homestead-<app>-admins` and `homestead-owner`.
Authentik then refuses anyone else before the app sees them. Bindings are
part of the app sign-in plan, so they are digest-bound, previewed and
audited (ADR-006). The dashboard's own application is bound to
`homestead-owner` too, on top of ADR-033's `(issuer, sub)` check. That is
defence in depth and does not replace the check.

Existing app sign-ins have no bindings. They keep working, and the Sign-in
page shows "open to every Authentik user" as a warning with a re-plan
offer. Nothing changes until the owner approves.

### 3. Roles inside apps
The app's provider gets a `groups` claim that lists only `homestead-`
groups. Each adapter maps `homestead-<app>-admins` to the app's admin
role:
- Nextcloud: the user_oidc group mapping, with that group as admin.
- Jellyfin: the SSO plugin's `AdminRoles` and `Roles`.
- Immich: its role claim.

An app that cannot map roles says so in the plan, and its admins stay
local. Each app's local admin account keeps working as break-glass.

### 4. People: Homestead is the primary way to maintain users
Homestead is where the owner maintains the household's users. Authentik
stores them and does the sign-in, but the owner does not need its admin UI
for anything about people. That UI is for break-glass only.

A person is an Authentik user created by Homestead. They are recorded in
SQLite with their Authentik user id and tagged in Authentik as
Homestead-managed. In Homestead the owner can, for the whole life of a
person:
- add a person, with a name, username and email, through a one-time
  enrollment link or a generated password the owner can reveal once;
- edit their name, email and username;
- reset their password, either with a new recovery link or a generated
  password revealed once;
- reset or remove their second factor;
- grant or revoke an app, as user or admin;
- disable a person, which blocks sign-in and keeps their data in the
  apps;
- re-enable a disabled person;
- remove a person, which deletes the Authentik user Homestead created.

The dashboard People page and `homesteadctl people …` use the same API
(parity). These are owner actions: a session plus confirmation, with
step-up for admin grants, password resets and removal, and every action is
audited without secrets. People are never Homestead administrators.

Users that already exist in Authentik but were not created by Homestead are
listed as "not managed by Homestead", with **Adopt**. Adopting tags them and
brings them under Homestead from then on. Authentik's own service and
system accounts are never listed.

### 5. The Authentik token
Managing people means Homestead must reach Authentik's API when the owner
acts, so the People feature needs the kept token. That is the existing
opt-in, sealed under ADR-021 (ADR-033 §Configuring). Turning on People asks
for that opt-in and says plainly what the token can do. Without a kept
token:
- the People page explains why it cannot act and offers to keep a token;
- group bindings are still applied with the use-once token at sign-in time.

### 6. Drift: Homestead's record wins
Homestead reads back its people, groups, memberships and bindings. Its own
records are authoritative. A change made in Authentik's UI to a managed
person or a `homestead-` group, such as a membership, a disabled flag or an
edited email, is shown as drift on People with the Homestead value beside
it. The recommended action is **Put back**, which restores Homestead's
record; **Keep** accepts the change into Homestead. Nothing is reverted
silently. A person deleted in Authentik shows as missing, with **Recreate**
or **Remove from Homestead**.

## Alternatives considered
- Leave Authentik open and only map roles: anyone in Authentik reaches every
  public app. Rejected by the owner.
- Manage people only in Authentik's UI, or in both as equals: there is no
  CLI or dashboard parity, and no single source of truth for who may sign
  in. Rejected by the owner: Homestead is primary.
- Grant Homestead administration from a group: still rejected (ADR-033,
  ADR-015). The claim, not the IdP, decides who owns the appliance.
- Edge forward-auth in front of each app: a separate decision; app-level
  OIDC plus bindings covers the need without it.

## Consequences
- The Authentik-side plan grows from two objects to groups, bindings and a
  property mapping per app. It stays idempotent and re-plannable.
- People need the kept Authentik token. A use-once owner still gets closed
  apps, but not the People page.
- New tables: people, app grants. New routes and CLI commands, which go in
  PARITY.
- Slices: S314 (groups, bindings, closed by default, role claims, existing
  sign-ins warned); S316 (People page and CLI covering the whole life of a
  person, adoption, drift with Homestead's record winning).
