# App Names, App Sign-in, and Nightly Backups

## Status
Accepted

Owner decisions 2026-10-20, from the S292 setup walkthrough findings and the
S295 questions. Builds on ADR-035 (local names), ADR-033 (Authentik),
ADR-023 (scheduler) and ADR-037 (public names). Records one exception to the
first-party catalog principle (§3, Jellyfin).

## Context
After setup, every app has a `*.lan` name that only resolves once the owner
adds it to router DNS or a hosts file; only `homestead.local` is announced
over mDNS. Immich, Nextcloud and Jellyfin declare optional OIDC sign-in, but
nothing wires them to the linked Authentik. Backups exist as per-app
interval schedules that setup never offers, so a fresh appliance has none.

## Decision

### 1. App names (S295)
- **`<app>.local` over mDNS, by default.** Each app route whose local name
  is `<label>.lan` is also announced as `<label>.local` by the in-process
  mDNS responder, next to `homestead.local`. The edge route for the app
  answers both names, and the local CA certificate covers both (ADR-030).
  Only single-label `.local` names are announced; multi-label names do not
  resolve on many clients. The responder's existing limits stay: IPv4 only,
  no probing or conflict arbitration (a name another device already
  announces is a doctor warning, not a fight).
- **An optional appliance resolver for `.lan`.** `homestead-server` can
  answer DNS for the appliance's own names (`homestead.lan` and each app's
  `<label>.lan`) on its LAN address, port 53, UDP and TCP. It is off by
  default and turned on through a plan the owner approves, which includes
  the firewall rule for port 53 from the local network. It answers only
  those names: other `.lan` names get NXDOMAIN, and every other query is
  REFUSED. It forwards nothing upstream. The owner adds a conditional
  forward for `lan` on the router (or points one device at it); the page
  says how. Disabling it removes the rule.
- Names are derived from the edge routes; there is no second list to keep
  in sync. The CLI and dashboard show each app's local names.

### 2. Nightly backups (S297)
- **One backup policy for all apps.** A single policy has a time of day
  (default 02:00, appliance local time), a retention count (default 7) and
  an on/off switch. It covers every stateful installed app, including apps
  installed later; stateless packages are skipped (ADR-023). The
  destination is the backup storage class.
- **Per-app schedules stay as overrides.** An app with its own schedule is
  backed up on that schedule and the policy skips it.
- **Setup proposes it.** A new setup step, Backups, after Apps, shows the
  proposed policy and what it covers. Nothing is scheduled until the owner
  approves it. Without a backup storage class the step explains that and
  links to Storage. Appliances that finished setup before the step existed
  have it waived (ADR-036 pattern).
- The scheduler gains a wall-clock job kind: next run at the first policy
  time after the last run. Missed runs (appliance off) run once at start,
  as interval jobs do today (ADR-026).

### 3. App sign-in through Authentik (S298)
- **Offered at install.** When Authentik is installed and dashboard sign-in
  is configured (ADR-033), the install wizard offers "Sign in with
  Authentik" for apps that declare OIDC, checked by default. The install
  plan shows what it creates. It can be added or removed later on the app
  page, each time through a plan the owner approves.
- **What it creates.** One Authentik OAuth2/OIDC provider and application
  per app, with redirect URIs for each of the app's names (`.lan`,
  `.local`, and public names once approved). The client secret is sealed
  in the secret store (ADR-021) and is never shown or logged. The app is
  then configured through a per-app adapter declared in its manifest:
  - **Immich:** its built-in OAuth settings.
  - **Nextcloud:** the `user_oidc` app, installed and configured with
    `occ`.
  - **Jellyfin:** the third-party `jellyfin-plugin-sso` plugin.
  Users are created in the app on first sign-in. The app's own local admin
  account keeps working, as the break-glass path.
- **Jellyfin exception.** Jellyfin has no built-in OIDC. Homestead installs
  `jellyfin-plugin-sso` at a pinned release whose checksum is recorded in
  the catalog, and refuses any other build. This is an exception to
  "first-party curated catalog only": it adds a third-party plugin inside a
  first-party package, not a third-party package. The plugin's version is
  updated only with a catalog update, and the app page names it.
- **Public names re-plan sign-in.** Approving a public name for an app
  (ADR-037) re-plans that app's provider to add the redirect URI; the
  owner approves the change. The same applies to the dashboard's own
  sign-in (ADR-037 §4, S296).
- Removing sign-in deletes the provider and application Homestead created
  and turns the app's OIDC setting off. App users created by sign-in stay.

## Consequences
- The mDNS responder serves a list of names; edge route aliases, which
  only the dashboard route had (ADR-035), are allowed on app routes for
  their `.local` name.
- The daemon may listen on port 53, only when the owner enabled the
  resolver. A resolver outage affects only the appliance's names.
- Backups gain a policy record and a wall-clock scheduler job. Per-app
  interval schedules are unchanged.
- The setup wizard gains a sixth step (Backups); stored setup payloads
  migrate with the step waived for finished appliances.
- Catalog manifests gain an OIDC adapter declaration; each adapter is
  first-party Go code. Jellyfin depends on a pinned third-party plugin.
- Slices: S295 names, then S296 (public dashboard), S297 nightly backups,
  S298 app sign-in.

## Alternatives Considered
- **Multi-label names (`<app>.homestead.local`):** many clients do not
  resolve them over mDNS.
- **A forwarding resolver the whole router points at:** makes the
  appliance the network's DNS; an outage would break every name. Rejected
  for v1.
- **AdGuard Home from the catalog:** another app to run for a few names.
  Rejected; the unwired rewrite code stays unused.
- **Per-app nightly schedules created automatically:** more records to
  manage and no single place to change the time. Rejected.
- **Leaving Jellyfin without sign-in:** rejected by the owner; the plugin
  exception is recorded instead.
