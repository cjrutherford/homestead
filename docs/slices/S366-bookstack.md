# S366: BookStack with Authentik sign-in

## Goal
Add the `bookstack` package (BookStack 26.09.2 plus a MariaDB database) and
sign people in through Authentik in the same slice (owner rule 2026-11-01:
one app per slice, sign-in included). It is the last Wave 3 slice and the
first package with a MariaDB service.

## Dependencies
S352 (MariaDB/MySQL dump and restore), S360 (community image trust, ADR-056),
S342/S357/S413 (secret files), S402/S412/S415-S417 (env-OIDC adapter, in-app
commands, guarded app-database writes), ADR-043, ADR-044, ADR-047, ADR-052
(with the S366 amendment), ADR-056.

## Scope
- `catalog/bookstack.yaml`: `lscr.io/linuxserver/bookstack:26.09.2`
  (publisher `linuxserver`) and `docker.io/library/mariadb:11.8.9` (upstream).
  Served on a name of its own (BookStack needs its root URL, so no path
  serving); local exposure recommended, wider allowed with a warning.
- Secrets only as files: `db_password` (MariaDB `MARIADB_PASSWORD_FILE`,
  BookStack `FILE__DB_PASSWORD`), `app_key` (`FILE__APP_KEY`),
  `oidc_client_secret` (`FILE__OIDC_CLIENT_SECRET`). No password, key or
  secret is in Compose environment. MariaDB runs with
  `MARIADB_RANDOM_ROOT_PASSWORD=yes`; no one is given the root password.
- New secret `encoding: laravel-key` (`internal/catalog/secrets.go`):
  BookStack's `APP_KEY` must be `base64:` plus 32 random bytes; Homestead's
  default generated value is 64 hex characters, which Laravel rejects. All
  other secrets stay hex. ADR-052 amendment records this.
- Sign-in: `sso.oidc` with `env-oidc`, exclusive (BookStack's login form and
  password reset are off while `AUTH_METHOD=oidc`). Env: `AUTH_METHOD=oidc`,
  `OIDC_NAME`, `OIDC_DISPLAY_NAME_CLAIMS=name`, `OIDC_CLIENT_ID`,
  `OIDC_ISSUER`, `OIDC_ISSUER_DISCOVER=true`, `OIDC_END_SESSION_ENDPOINT=true`,
  `OIDC_USER_TO_GROUPS=true`, `OIDC_GROUPS_CLAIM=groups`,
  `OIDC_REMOVE_FROM_GROUPS=true`. Callback `/oidc/callback`, sign-in page
  `/login`. Trust of the appliance CA: `trustbundle: system` over
  `/etc/ssl/certs/ca-certificates.crt` (private issuer only).
- Roles: BookStack matches a group to a role by the role's External
  Authentication IDs, which environment cannot set. A guarded `onapply`/`onoff`
  PHP script (run with the image's own `php`) writes `roles.external_auth_id`
  for the Admin role (`<app admin group>,homestead-owner`) and the Editor
  role (`<app user group>`). Admins and the owner become Admins, everyone else
  Authentik admits an Editor. The group names reach the script as
  `HOMESTEAD_ADMIN_GROUP` / `HOMESTEAD_USER_GROUP`, set from the `{{admin_group}}`
  / `{{user_group}}` templates and removed again on off.
- Script guards (ADR-052 amendments): version guard on `/app/www/version`
  (on), schema guard (`information_schema.COLUMNS` for the five columns
  used), waits until every migration file is recorded in `migrations`, one
  transaction with `FOR UPDATE`, idempotent, refuses (rolls back, exit 1)
  when a role already carries External Authentication IDs of its own, off
  clears only values equal to what on wrote, no secret in argv or output,
  generic error text, no `getMessage`.
- Default administrator: BookStack seeds `admin@admin.com` / `password`. A new
  first-run routine `bookstack` (`internal/firstrun/bookstack.go`) replaces
  that password at install with a generated, revealable `admin_password`,
  with a compare-and-set that applies only while `password` still opens the
  account, so a changed or deleted account is never touched. Password on
  stdin; it waits for migrations. It works the same with sign-in off.
- Backup: S352 `database` block (`kind: mariadb`, service `db`) plus
  `backuppaths: [config]` (uploads, `.env`, themes).
- Integrations: identity to Authentik.
- Out of scope (DEFERRED): mail, LDAP, SAML, per-shelf permissions, a
  Homestead-run password reset for the break-glass account.

## Acceptance
- `apps plan bookstack` / `apps install bookstack` work; the second container
  is MariaDB with a data volume marked `database: true`.
- Compose has no plaintext password, key or secret; secrets are `FILE__*` /
  `*_FILE` files, 0600 and owned by their consumer.
- `APP_KEY` is `base64:` plus 32 bytes and never regenerated.
- After install, `admin@admin.com` / `password` is refused; the revealed
  password works with sign-in off.
- Sign-in plan shows the `AUTH_METHOD` etc. keys, the PHP script (as
  `<n-line script>`), the exclusive login warning, the role mapping and the
  email-clash warning. Apply sets roles; off clears exactly what apply wrote.
- Re-running apply or off changes nothing; a role with the owner's own
  External Authentication IDs makes apply refuse and writes nothing.
- A version change fails `TestBookStackSignInPinnedToVerifiedVersion` until
  the script is re-verified.

## Tests
- `internal/appsso/bookstack_test.go`: pin test, script guards, plan
  (redirects, warnings, settings, off plan), env rendering (no secret),
  trust mount, enable/disable commands.
- `internal/firstrun/bookstack_test.go`: wait/create, ready (left alone),
  redaction, unsupported account, script guards.
- `internal/catalog`: curated lists, SSO posture, image volumes,
  `TestLaravelKeyEncodingAndBookStackSecrets`.
- `tests/test_s366_bookstack.py`: manifest and docs.
- Run against real containers on 2026-11-01 (docker on the build host,
  `lscr.io/linuxserver/bookstack:26.09.2` + `mariadb:11.8.9`, compose-like
  env): the role script on/on/off/off (idempotent, restores empty), refusal
  on an existing value (rc 1, nothing written), bad argument refused; the
  first-run script created, then reported ready twice (also with a
  non-bcrypt hash). Confirmed: `php` at `/usr/bin/php`, `/app/www/version`
  `v26.09.2`, `/oidc/callback` route, `/etc/ssl/certs/ca-certificates.crt`
  exists, `FILE__APP_KEY` exported as `APP_KEY` by the init, `.env` is
  created with `APP_KEY=SomeRandomString` which the environment overrides.

## Built
See the files above. Also `FirstRunBookStack` in the catalog schema and the
`Encoding` field on `SecretDecl`/`SecretFile`.

## Image trust (ADR-056 section 7)
- `lscr.io/linuxserver/bookstack:26.09.2`, publisher `linuxserver` (the one
  named LinuxServer package set; ADR-056): platforms linux/amd64 and
  linux/arm64; manifest-list digest
  `sha256:da6324232770050b7d5f6222e8f4e223f1c688363b02ebb3c15a7546dd7133d5`
  (checked 2026-11-01); image built 2026-10-28; source
  `github.com/linuxserver/docker-bookstack` (Dockerfile) over BookStack
  `github.com/BookStackApp/BookStack` (MIT; image labelled GPL-3.0-only);
  tag scheme `<version>` bare (also `v<version>-ls<N>`, `version-v<version>`,
  all verified identical for 26.09.2); the bare tag is pinned so update
  discovery (S343) parses it. No cosign signature or attestation is published
  (not checked by tag); trust is by LinuxServer being the named publisher.
- `docker.io/library/mariadb:11.8.9`, upstream (Docker official image):
  linux/amd64 and linux/arm64/v8; digest
  `sha256:c4124d1afa875e47f2892edf26b482fa54f796e2f342d3717a04ca091a22a9bd`
  (checked 2026-11-01); source `github.com/MariaDB/mariadb-docker`; GPL-2.0;
  11.8 is the current LTS line; Docker Content Trust not relied on.

## Sources
BookStack docs (OIDC and LDAP authentication, installation env), BookStack
v26.09.2 source (`app/Config/oidc.php`, `app/Access/GroupSyncService.php`,
`routes/web.php`), `linuxserver/docker-bookstack` README (`FILE__` secrets,
`APP_URL`, `/config`), MariaDB Docker docs (`*_FILE`, random root password).

## Verify live
On VM 2 (`BS=bookstack-bookstack`, `DB=bookstack-db`, `<server>` the edge
name, `<name>` BookStack's own name). Not run in this slice.
1. Install: write `bookstack.json` as for any package (the install JSON
   pattern in `docs/INSTALL.md`; BookStack has no required variables, so `{}`
   plus the usual `volumes` for application storage). `homesteadctl apps plan
   bookstack -f bookstack.json` prints the install, sign-in and access
   digests. Then `homesteadctl apps install bookstack -f bookstack.json
   --approve-sign-in <sign-in digest> --approve-access <access digest>
   --follow` (add `--public --approve-public <digest>` only to publish it).
2. Containers: `docker ps` shows `$BS` and `$DB`; `docker logs $BS 2>&1 |
   grep -E "FILE__APP_KEY|DONE" | tail` shows `APP_KEY set from FILE__APP_KEY`
   and the migrations; `docker logs $DB 2>&1 | tail -3` shows it ready.
3. Default admin gone: in a browser (sign-in still off, or after step 8's off)
   try `admin@admin.com` / `password`; it must fail. Reveal the generated one:
   `homesteadctl apps secrets reveal bookstack admin_password` and sign in.
4. Database (password read from the mounted file, not typed):
   `PW=$(docker exec $BS sh -c 'cat "$FILE__DB_PASSWORD"')` then
   `docker exec $DB mariadb -ubookstack -p"$PW" bookstack -N -e "select
   display_name,system_name,external_auth_id from roles"` (empty IDs before
   sign-in) and `... -e "select count(*) from migrations"`.
5. Plan sign-in: `homesteadctl apps sign-in plan bookstack`; read the warnings
   (login form off, Editor mapping, email clash, off consequences) and note
   the digest. Apply: `homesteadctl apps sign-in apply bookstack --digest
   <digest> --follow`.
6. After apply: the roles query shows Admin
   `homestead-bookstack-admins,homestead-owner` and Editor
   `homestead-bookstack-users`. OIDC redirect:
   `curl -sk https://<name>/login | grep -i -E "oidc|Authentik"` shows the
   Authentik button; in a browser the button lands on
   `<issuer>authorize/?...redirect_uri=https://<name>/oidc/callback`.
   Discovery and CA trust from inside the app: `docker exec $BS curl -s
   <issuer>.well-known/openid-configuration | head -c 200`.
7. Browser: sign in as the owner (Admin) and as a person in the app's users
   group (Editor); remove that person's Authentik access and sign in again
   (refused). Check the signed-out person lands on Authentik's end-session.
8. Re-apply is a no-op (same roles). Set the Editor role's External
   Authentication IDs to something else by hand, then plan and apply again;
   apply must fail naming the role, with nothing written.
9. Backup: `homesteadctl backup schedule bookstack --interval 1 --retain 2`
   (see `homesteadctl backup schedule`), wait for a run in the dashboard's
   backup page or `homesteadctl backup schedules`, then check the run's
   artifacts: a `mariadb-dump` of database `bookstack` (contains `CREATE
   TABLE \`roles\``) and the `config` volume.
10. Sign-in off: `homesteadctl apps sign-in plan bookstack --off`, apply with
    its digest. The login form returns, the roles' External Authentication
    IDs are empty again, and the revealed admin password signs in.
