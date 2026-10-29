# Post-claim setup wizard (S287, S291, S297, ADR-034, ADR-036, ADR-039)

After claim and first-administrator creation the dashboard walks a new owner
through six steps, one on screen at a time: secure the owner account, storage,
sign-in, domain and access, first apps, nightly backups, then a summary. ADR-036 (S291) turned
the ADR-034 walkthrough into this wizard. Each step does its work inline by
embedding the same pages and calling the same API routes as the standalone
pages, which stay. The wizard is still orchestration only: it reads what is
true on the appliance, records the owner's skips, acknowledgements and
non-sensitive choices, and every change still goes through its own plan,
review and approve flow.

## Wizard behaviour

- Linear: Next is enabled only when the step is done or skipped. Back always
  works. The wizard opens at the first pending step, or on the summary when
  none is pending. The progress list marks the current step with
  `aria-current="step"` and shows each state as a word.
- On a step change focus moves to the step heading.
- A step that is finished through an embedded page (an app class assigned, an
  install finished) is noticed without a reload: the wizard re-reads
  `GET /api/v1/setup` after any successful non-setup write.
- Every step shows "What this does" and an ordered "Outside Homestead"
  checklist of tasks done in other tools.
- The summary lists each step's outcome with a button back to any step.
- The progress list is not clickable; use Back, Next and the summary.

Menu paths for the outside tasks (Cloudflare, Authentik) were written
2026-10-20 from vendor documentation (Authentik 2025.x); to be confirmed
against the live UI in the S291 KVM test. Vendors rename menus.

An appliance whose setup was finished before the storage step (or the
backups step, S297) existed has that step recorded as waived (not an owner skip). It reads "Not required",
offers no Undo skip, and never re-gates the appliance.

### Outside Homestead, per step

| Step | Tasks, in order |
| --- | --- |
| Account | keep the password in a password manager; store the recovery file offline |
| Storage | the disk must already be mounted (Homestead never formats); an `/etc/fstab` example is shown |
| Sign-in | reach `authentik.<name>`; trust the root certificate (Trust page); open `https://authentik.<name>/` (Authentik 2026.8 starts its first-run only from the address itself) and create `akadmin`; Admin interface, Directory, Tokens and App passwords, New Token, user `akadmin`, API Token (checked against Authentik 2026.8.3 on 2026-10-20, S292) |
| Domain | add the domain to Cloudflare; create the scoped token with the permissions below (the tunnel reuses it, S294); the other providers' equivalents; for manual DNS, the record table |
| Apps | none |
| Backups | assign the backup storage class to a mounted disk first (Storage page) |

## The steps and how each is derived

State is `pending`, `done` or `skipped`, computed on every read from real
state. Real state wins over a recorded skip: a skipped step that is then
finished from the CLI or another page reads `done`.

| Step | Done when | Otherwise |
| --- | --- | --- |
| `account` | the latest recovery identity export is acknowledged (`backups/identity`) and the owner confirmed their password | pending; skippable |
| `storage` | the application storage class is assigned (`storage_app_assigned`); real state wins, so assigning it from the CLI or the Storage page reads `done`. Skippable; `PUT .../steps/storage {"state":"done"}` answers 400 | pending |
| `sign-in` | Authentik sign-in is configured and linked (`sso` status); a dormant link (Authentik removed) stays done and the detail warns that Authentik sign-in is off and the local password still works; a sign-in that does not yet cover the tunnel's public dashboard address stays done and the detail (here and on the Domain step) says to re-plan it (S304) | pending; skippable |
| `domain` | see "Domain completion" below | pending; skippable |
| `apps` | the owner acknowledged it, or any catalog app other than Authentik is installed | pending; skippable |
| `backups` | a nightly backup policy is approved, on or off (S297, ADR-039 §2). The step shows the proposed policy (02:00 local, keep 7) and the apps it covers and leaves out; without backup storage it explains and links to the Storage page. `PUT .../steps/backups {"state":"done"}` answers 400 | pending; skippable |

The walkthrough is complete when every step is done or skipped.

Step 1 has two parts. The recovery identity is exported and acknowledged
on the Backups page, through the existing `backups/identity` routes. The password is confirmed by
the owner explicitly ("Confirm my password", beside a link to the Account
password change; CLI `setup step account --acknowledge`). The daemon has no
generated or temporary password state to detect, so this is an acknowledgement
(tracking such passwords is in `docs/DEFERRED.md`). The step is done only when
both are true. Taking the step back (`unskip`) clears the confirmation.

### Domain completion

What the owner chose (`access`) decides what the step needs:

| Access | Confirming needs |
| --- | --- |
| Home network only (default) | the acknowledgement alone |
| Cloudflare Tunnel | Cloudflare as the stored provider (the choice here, else the one domain onboarding saved) and a tunnel route (`Facts.Tunnels > 0`) |
| Router port forward | a stored provider and an approved exposure plan (`Facts.Exposures > 0`) |

`PUT /api/v1/setup/steps/domain {"state":"done"}` answers 422 with a plain
message when those facts are missing, naming the page that fixes it (`/admin/tunnel`
or `/admin/exposure`). The dashboard reads the same facts and explains what is still
needed instead of offering "I have finished here". An acknowledgement made
under home network only does not satisfy a later tunnel or router choice. A
tunnel route or approved exposure plan that exists already counts as done, as
does an onboarded domain under home network only. The step's detail text
reflects the chosen access.

This is how "reviewed and approved in the walkthrough" is met: the plan is
reviewed and approved on the linked Tunnel or Exposure page (those pages own
plan, review and approve, ADR-006), and the walkthrough reads back the approved
state before it lets the owner confirm.

The domain step has a review mode. When a domain is onboarded, or a tunnel
route or an approved exposure plan already exists (a saved but never-onboarded
domain wizard payload does not count),
the step opens as a review of it: links to the Tunnel, Exposure and
Certificates pages, and a "This looks right" button that only records the
acknowledgement. The walkthrough never re-runs onboarding or re-creates a
tunnel.

## Provider and access choices

The domain step offers Cloudflare, DigitalOcean, Amazon Route 53 and Manual.
For each, the page lists the credential fields (read from the adapter's
`RequiredCredentials`, so the form cannot ask for something the adapter does
not read) and the exact permissions. The permission copy is pinned by
`internal/setup/permissions_test.go`: each adapter runs against a recording
server and the test fails if the operations it makes and the grants listed
differ in either direction. The Cloudflare tunnel grant (`Account:
Cloudflare Tunnel: Edit`) is pinned the same way by
`TestTunnelPermissionCopyMatchesClientCalls`, which runs the tunnel client
(zone lookup, DNS record list, create and delete, list accounts, tunnel
create, get and delete) against a recording server; every call maps to a
listed grant (S294). The grant
wording is our description of the provider console, not verified console text;
the set of grants is what is pinned.

| Provider | Permissions |
| --- | --- |
| Cloudflare | Zone: Zone: Read; Zone: DNS: Edit (also the tunnel hostname's CNAME); plus Account: Cloudflare Tunnel: Edit when a tunnel is chosen. One token: the tunnel never asks for it again or for an account ID |
| DigitalOcean | a token with Read and Write scope |
| Route 53 | `route53:ListHostedZones`, `route53:ListResourceRecordSets`, `route53:ChangeResourceRecordSets` |
| Manual | none; Homestead shows the records and you create them |

Domain (S294): the public domain, a non-sensitive choice saved with "Save
domain" or `setup step domain --domain NAME` (empty clears it). Local-only
suffixes (`lan`, `local`, `home.arpa`, `internal`, `localhost`) and dotless
names are refused with 422. With Cloudflare as the provider and its token
stored, a domain no zone the token can see contains is also 422; without a
token yet, or when Cloudflare cannot be reached, it is saved and the tunnel
plan reports the zone. The tunnel form proposes `homestead.<domain>` for
the dashboard and `auth.<domain>` for Authentik; the owner may rename
either. The tunnel itself is planned (`tunnel plan`), reviewed and approved
by digest; see ADR-037. Every public name goes to the edge. The dashboard
name is left out, with the reason, until sign-in with Authentik is linked
(S296); once it is published, the plan says to re-plan Sign-in, and on the
public name only Authentik signs in.

Access: home network only (default), Cloudflare Tunnel, router port forward.
The tunnel option is available only with Cloudflare (ADR-017); for other
providers it stays in the list, unavailable, with the reason. DigitalOcean,
Route 53 and manual cover DNS and certificates only; reaching the appliance
from outside is a router port forward. Droplet and relay tunnels, other DNS
providers and router adapters are in `docs/DEFERRED.md`.

The credential is typed into the walkthrough form and sent straight to
`POST /api/v1/tls/issuance/credentials`, which seals it in the secret store
(ADR-021) as `dns_<provider>_<field>`. The form is cleared as soon as the
request is sent. The walkthrough reads only whether each credential is
present. No credential value is in a wizard payload, an audit detail, a log or
any setup response; `internal/server/setup_test.go` proves it with a sentinel
value.

### Appliances set up before a step existed

The stored record carries `version`: 2 added storage (S291), 3 added backups
(S297). A record older than a step whose older steps were all done or skipped
gets the newer steps added to its waived list the first time it is read or
updated, so a complete appliance is never re-gated by a new step
(`TestPreStorageApplianceIsNotRegated`, `TestPreBackupsApplianceIsNotRegated`).
Legacy appliances stay ungated.

## Where state lives

The wizard store (`wizard_state`, id `setup`) holds only: `origin`, `skipped`,
`acknowledged`, `provider`, `access` and `version`. The step shown after a reload is the
first one still pending. The generic `PUT` and `DELETE /api/v1/wizards/setup`
answer 403; the walkthrough changes only through `/api/v1/setup`.

## Gating and the legacy stamp

The gate is a router guard in the dashboard and nothing else. The API, the CLI
and the acceptance script are never gated; no route reads the `gated` field.

For a fresh appliance the guard redirects every dashboard route to `/setup`
until the walkthrough is complete, except the pages below. Each is exempt because the walkthrough sends the owner there:

| Route | Why it is exempt |
| --- | --- |
| `/setup` | the walkthrough itself |
| `/login`, `/claim` | the way in; the owner must be able to sign in or claim |
| `/account` | change the password |
| `/admin/backups` | export and acknowledge the recovery identity (step 1) |
| `/sign-in` | link or configure Authentik sign-in (step 2) |
| `/admin/tunnel` | create and approve a tunnel (step 3) |
| `/admin/exposure` | review and approve an exposure plan (step 3) |
| `/admin/tls` | certificates and DNS credentials (step 3) |
| `/admin/storage` | register mounted storage, which the install wizard needs before any app can be installed (steps 2 and 4) |
| `/admin/edge` | the install wizard's requirement step links here for the edge proxy |
| `/admin/trust` | the install wizard's address step links here for the Homestead root certificate |
| `/catalog/:name/install` | the S285 install wizard for Authentik and starter apps |
| `/admin/operations`, `/admin/operations/:id` | follow an install the walkthrough started, from the rail or the list |

The guard fails open: it lets a navigation through when the owner is not
logged in or the status cannot be read (any error from `GET /api/v1/setup`), so
a daemon fault can never lock the owner out of the dashboard.

An appliance claimed before this slice is not gated. Schema migration 44
inserts the `setup` wizard row `{"origin":"legacy"}` only when the appliance is
already `CLAIMED` at the moment the migration runs. A new, unclaimed appliance
and a factory-reset one get no row and read `origin: fresh`; so do test
fixtures and the acceptance flow, which create a store and claim afterwards.
Legacy appliances see a "Finish setup" navigation entry while steps are
unfinished; the entry is called "Setup" otherwise, and stays after completion.

## Factory reset

`scripts/factory-reset.sh --yes [--wipe-mount PATH]... [--reinstall]` (S306)
removes the database, `homestead.db` with its `-wal` and `-shm` files, along
with the rest of Homestead's state; see `docs/INSTALL.md` Part F. The `setup`
wizard row and the legacy stamp live only in that database's `wizard_state`
table, so they cannot survive a reset. Migration 44 stamps `legacy` only when
the appliance is already `CLAIMED` at the moment the migration runs; a new
database migrates while unclaimed, gets no `setup` row, and a freshly claimed
appliance reads `origin: fresh` (`TestFreshStoreHasNoSetupRow`). A reset that
keeps the database and only clears claim state must also delete
`wizard_state` where `wizard = 'setup'`.

## Operating it from the CLI

```
homesteadctl setup status            # origin, the six steps, what is next
homesteadctl setup step domain       # one step's state and detail
homesteadctl setup step domain --provider cloudflare --access tunnel
homesteadctl setup step domain --domain example.com
homesteadctl setup step account --acknowledge  # confirm my password
homesteadctl setup step apps --acknowledge     # also domain, once its tunnel/exposure exists
homesteadctl setup skip sign-in      # storage and backups too: skip and unskip only
homesteadctl backup policy           # the backups step: proposal and coverage
homesteadctl backup policy plan --time 02:00 --retain 7
homesteadctl backup policy apply --time 02:00 --retain 7 --digest <digest>
homesteadctl setup unskip sign-in
```

Resuming is automatic: the dashboard opens on the first pending step after a
reload or a new login, and `setup status` shows the same answer. The CLI
commands change only the walkthrough's record; credentials and plans use their
own commands (`tls issuance credentials`, `sso ...`, tunnel and exposure
commands, `apps install`).
