# S392 — Komga Authentik integration prerequisite

## Status

Fresh-install local recovery prerequisite implemented; the per-person adapter
remains blocked at the pinned upstream authentication contract. No Komga adapter,
automatic OIDC registration, or per-person access enforcement is enabled by
this slice. `catalog/komga.yaml` remains an **optional, owner-configured**
OIDC declaration from S355, not an actuated SSO integration. The pinned image
is `docker.io/gotson/komga:1.29.1`.

## Required outcome

The owner must retain full Komga access and a working local recovery
administrator. Every other managed person needs an enforceable `admin`,
`user`, or `none` decision at installation and on later changes. Activation
must preserve existing accounts, passwords, reading progress, library and
user settings, and native clients unless the owner explicitly approves a
specific credential migration. A successful web OIDC login alone is not
proof of this outcome.

## Verified upstream behavior in 1.29.1

- The [official configuration reference](https://komga.org/docs/installation/configuration/)
  says the Docker image reads `/config/application.yml`. It supports a
  Spring OIDC client registration and `komga.oauth2-account-creation`, which
  defaults to `false`. Enabling account creation gives new users a random
  local password that they can later change for OPDS or other clients.
  The [social login guide](https://komga.org/docs/installation/oauth2/)
  requires a verified email and describes email matching. A client secret
  would therefore belong in a protected file under the existing `/config`
  mount, not Compose environment or process arguments. File custody is
  feasible, but does not solve authorization.
- The pinned
  [OIDC user service](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/infrastructure/security/oauth2/KomgaOAuth2UserServiceConfiguration.kt)
  looks up a Komga user by case-insensitive email, or creates one when the
  setting above allows it. It does not read group or role claims to grant,
  demote, or deny that user. Existing account settings and roles are the
  source of authority. Email matching alone could attach an Authentik
  identity to an existing Komga administrator; this needs an explicit,
  owner-approved link and a verified identity binding before actuation.
- The pinned
  [principal](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/infrastructure/security/KomgaPrincipal.kt)
  grants authorities from stored `user.roles` and returns `true` from
  `isEnabled`. The
  [security configuration](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/infrastructure/security/SecurityConfiguration.kt)
  enables HTTP Basic, API key filters, remember-me, and OIDC together. The
  [user-details service](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/infrastructure/security/KomgaUserDetailsService.kt)
  still loads existing local users by email. Turning off automatic OIDC
  account creation prevents *new* OIDC accounts; it does not disable an
  existing user's password, sessions, or API keys.
- The pinned
  [user controller](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/interfaces/api/rest/UserController.kt)
  lets an administrator change stored roles and passwords, but the
  [user model](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/domain/model/KomgaUser.kt)
  has no enabled/disabled flag. Removing roles is not a `none` decision:
  the security configuration still accepts an authenticated user for many
  `/api/**` and `/opds/**` routes. Deleting the account would also delete
  account-linked data in the
  [user lifecycle](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/domain/service/KomgaUserLifecycle.kt),
  contrary to the preservation requirement.
- The [claim endpoint](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/interfaces/api/rest/ClaimController.kt)
  creates the first local administrator only while no users exist. The
  [official client documentation](https://komga.org/docs/guides/kasane/)
  describes OPDS Basic Auth with Komga credentials, and the
  [Mihon guide](https://komga.org/docs/guides/mihon/) likewise uses a
  username and password. Blocking those paths at the reverse proxy would
  break native clients without changing Komga's own authorization.

## Decision against the requested contract

| Decision | Pinned Komga behavior | Result |
| --- | --- | --- |
| Owner full | A claimed local administrator can retain full access. | Recovery is possible, but must be established and tested before changes. |
| `admin` | OIDC email linking inherits stored Komga roles; no verified group-to-admin claim mapping. | Cannot grant exactly the approved administrator role safely. |
| `user` | A matching existing administrator retains `ADMIN`; OIDC does not demote it. | Cannot enforce user-only access. |
| `none` | Existing password, session, and API-key paths remain available; an authenticated account remains enabled. | Cannot revoke access while preserving the account. |

## Smallest concrete prerequisite

The Komga install now checks that its resolved `/config` mount is absent or
empty before any provisioning step. After the pinned image starts, it reads
`GET /api/v1/claim`, refuses a claimed database, seals a recovery password,
claims `homestead-recovery@localhost.invalid` through the supported claim API,
then verifies the same account has `ADMIN` via authenticated
`GET /api/v2/users/me`. The password enters the container through `docker
exec` stdin and curl's config input, never argv or logs. Failed verification
halts the install without deleting data; a subsequent attempt refuses the
now-claimed database for manual inspection. This is only a local recovery
account. It does not provision people or enable OIDC. An external actor with
write access to the config mount during installation can race the emptiness
check; a live disposable Komga run is still required before deployment.
S390 currently classifies Komga as an unavailable managed install, so the
install API does not expose this prerequisite yet. Do not mark Komga
installable with person choices until the per-person lifecycle can enforce
later changes and `none`.

The pinned [claim controller](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/interfaces/api/rest/ClaimController.kt)
supports this first-account action. The pinned
[user controller](https://github.com/gotson/komga/blob/1.29.1/komga/src/main/kotlin/org/gotson/komga/interfaces/api/rest/UserController.kt)
supports listing users, creating users with stored roles, and updating those
roles. These actions would allow a fresh, explicitly granted account to be
created before OIDC, with account creation disabled for everyone else. They
do not enforce a later `none` decision for an existing account: its Basic
credentials, sessions and API keys can still authenticate, and removing its
roles does not disable it. The existing per-person lifecycle therefore cannot
truthfully report revocation, so no Komga adapter is declared.

Before implementing an adapter, obtain a supported Komga 1.29.1-compatible
authentication and authorization mechanism that (1) binds an Authentik
subject to an explicitly approved existing account without relying on email
alone, (2) enforces `none` for the entire account across OIDC, Basic,
sessions, remember-me, API keys and native OPDS clients while retaining its
data, (3) enforces `user` versus `admin` from approved Authentik membership
without inheriting a stale stored administrator role, and (4) exempts only
the explicitly established local recovery administrator. This may require
an upstream feature or a separately reviewed Komga-specific extension.
Do not substitute a proxy-only gate or direct SQLite mutation for this
contract.

With that prerequisite available, implement a protected, atomic merge of
the managed Spring registration into `/config/application.yml`, preserve
unrelated settings, enforce mode `0600`/appropriate ownership, avoid secret
values in logs and arguments, use the exact callback, and test idempotence,
rollback, and restarts. Then add a first-party adapter and manifest
declaration, with focused tests for fresh claim, existing accounts, admin
demotion, `none` across every credential type, owner recovery, and native
OPDS access. A live disposable Komga 1.29.1 plus Authentik end-to-end test
must confirm the result before advertising access enforcement.

## Evidence and boundaries

Source inspection used the immutable `1.29.1` tag in the links above. No
Komga or Authentik runtime was started, no appliance was changed, and no
runtime access claim is made. The Gitea prerequisite in S391 and S390's
server/publication work remain separate.
