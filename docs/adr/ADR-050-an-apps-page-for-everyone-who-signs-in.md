# An Apps Page for Everyone Who Signs In; Administration Moves to /admin

## Status
Accepted

The owner chose each decision below on 2026-10-28. Amends ADR-033 (only
the linked owner may sign in to the dashboard), ADR-037 (who may sign in on
the public dashboard name) and ADR-043 §2 (the dashboard's Authentik
application is bound to `homestead-owner` only).

## Context
The dashboard is an administration console. People added through the
People page (ADR-043) reach their apps only by knowing each app's
address. There is no page that shows a person the apps they may use, and
the owner lands on the status page, not on their apps.

Today only the linked owner's Authentik account may sign in to the
dashboard. Any other subject is refused as `unlinked`.

## Decision
### 1. People may sign in to Homestead, with a person session
Anyone on the People page signs in to the dashboard with Authentik, on
every dashboard name (local, overlay and public):
- The callback matches the Authentik subject to a People record through
  Homestead's own record of that person's Authentik user, never by a
  username or email claim alone. A subject that is neither the linked
  owner nor a matched, active person is refused as today (`unlinked`).
- A matched person gets a **person session**. The linked owner gets the
  owner session they get today. The local password still signs in only
  the owner, and only on local and overlay names (ADR-037 §4).
- The person session's role is stored with the session and cannot be
  raised. A person has no step-up (ADR-033, ADR-049); step-up routes refuse
  person sessions.
- Sign-in and sign-out of people are audited, naming the person.
- Removing or disabling a person ends their sessions.

### 2. Person sessions are deny-by-default
The boundary refuses a person session on every route except a short
allowlist: who am I, my apps, sign-out, and the static dashboard. Other
routes return 403 with the code `owner-only`. Tests enumerate every
registered route, so a new route is owner-only unless it is added to the
allowlist on purpose.

### 3. Authentik lets people reach the dashboard
Homestead creates and owns a `homestead-people` group (ADR-043 §1 prefix
rules). Its membership is every active person Homestead manages. The
dashboard's Authentik application is bound to `homestead-owner` and
`homestead-people`. The group and the binding are part of the sign-in plan
and its digest. Appliances set up earlier show a re-plan reason. Nothing
changes until the owner approves.

### 4. The apps page is the dashboard's root
- `/` is the apps page for everyone who signs in. Signed-out visitors are
  sent to sign in, on every name. On the home network too, nobody sees
  the list without signing in.
- A person sees the installed apps they are granted (users or admins
  groups). The owner sees every installed app with a web address.
- Each tile shows the app's icon and name and opens the app. The address
  matches how the dashboard was reached: the app's public address on the
  public name when the app is published, otherwise its local address.
- The owner also sees an "Administration" link. Every administration
  screen moves under `/admin/…`. Old paths redirect to their `/admin/`
  path. Setup, claim and sign-in stay where they are.

### 5. Parity
The apps list comes from one API used by the dashboard and the CLI. The
CLI runs as the owner. It shows the owner's list and, for any person, the
list that person would see, so the owner can check grants without signing
in as them.

## Alternatives considered
- **Owner-only apps page:** smaller, but people would still have no
  place to find their apps. Not chosen.
- **Apps page signed out on the home network:** easier on the LAN, but
  not scoped to a person and shows every app to every guest. Not chosen.
- **A new page beside the existing admin URLs:** smaller change, but the
  root would stay an admin screen. Not chosen.
- **Match people by username or email claim:** both can be edited in
  Authentik by an Authentik admin. Rejected.

## Consequences
- Non-owners can hold dashboard sessions. The boundary gains a role check
  and a route allowlist, enforced by tests.
- The public dashboard name accepts sign-ins from people, not only the
  owner. Management stays owner-only (ADR-037).
- The sign-in plan gains the `homestead-people` group and binding.
- Bookmarks to admin screens keep working through redirects.
- Changes require a superseding ADR.

## Implementing slices
In order, one commit each:
1. S337: the apps list API for the owner and per-person preview, and the
   CLI (§4 contents, §5).
2. S338: person sign-in, person sessions, the deny-by-default boundary
   and the `homestead-people` group and binding (§1–§3).
3. S339: the dashboard: the apps page at `/`, administration under
   `/admin/` with redirects, and the person view (§4).
