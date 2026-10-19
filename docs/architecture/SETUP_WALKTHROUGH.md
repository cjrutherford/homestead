# Post-claim setup walkthrough (S287, ADR-034)

After claim and first-administrator creation the dashboard walks a new owner
through four steps: secure the owner account, sign-in, domain and access, first
apps. It is orchestration only. It reads what is true on the appliance, records
the owner's skips, acknowledgements and non-sensitive choices, and sends the
owner to the pages that already make each change. Nothing is applied by the
walkthrough itself; every change still goes through its own plan, review and
approve flow (the S285 install wizard, the S286 sign-in flow, the Tunnel and
Exposure pages).

## The steps and how each is derived

State is `pending`, `done` or `skipped`, computed on every read from real
state. Real state wins over a recorded skip: a skipped step that is then
finished from the CLI or another page reads `done`.

| Step | Done when | Otherwise |
| --- | --- | --- |
| `account` | the latest recovery identity export is acknowledged (`backups/identity`) and the owner confirmed their password | pending; skippable |
| `sign-in` | Authentik sign-in is configured and linked (`sso` status); a dormant link (Authentik removed) stays done and the detail warns that Authentik sign-in is off and the local password still works | pending; skippable |
| `domain` | see "Domain completion" below | pending; skippable |
| `apps` | the owner acknowledged it, or any catalog app other than Authentik is installed | pending; skippable |

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
message when those facts are missing, naming the page that fixes it (`/tunnel`
or `/exposure`). The dashboard reads the same facts and explains what is still
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
(list accounts, create, get, delete) against a recording server. The grant
wording is our description of the provider console, not verified console text;
the set of grants is what is pinned.

| Provider | Permissions |
| --- | --- |
| Cloudflare | Zone: Zone: Read; Zone: DNS: Edit; plus Account: Cloudflare Tunnel: Edit when a tunnel is chosen |
| DigitalOcean | a token with Read and Write scope |
| Route 53 | `route53:ListHostedZones`, `route53:ListResourceRecordSets`, `route53:ChangeResourceRecordSets` |
| Manual | none; Homestead shows the records and you create them |

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

## Where state lives

The wizard store (`wizard_state`, id `setup`) holds only: `origin`, `skipped`,
`acknowledged`, `provider` and `access`. The step shown after a reload is the
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
| `/backups` | export and acknowledge the recovery identity (step 1) |
| `/sign-in` | link or configure Authentik sign-in (step 2) |
| `/tunnel` | create and approve a tunnel (step 3) |
| `/exposure` | review and approve an exposure plan (step 3) |
| `/tls` | certificates and DNS credentials (step 3) |
| `/storage` | register mounted storage, which the install wizard needs before any app can be installed (steps 2 and 4) |
| `/edge` | the install wizard's requirement step links here for the edge proxy |
| `/trust` | the install wizard's address step links here for the Homestead root certificate |
| `/catalog/:name/install` | the S285 install wizard for Authentik and starter apps |
| `/operations`, `/operations/:id` | follow an install the walkthrough started, from the rail or the list |

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

There is no in-repo factory-reset command or script. Resetting an appliance
means removing its database, `homestead.db` (with its `-wal` and `-shm`
files). The `setup` wizard row and the legacy stamp live only in that
database's `wizard_state` table, so they cannot survive a reset. Migration 44
stamps `legacy` only when the appliance is already `CLAIMED` at the moment the
migration runs; a new database migrates while unclaimed, gets no `setup` row,
and a freshly claimed appliance reads `origin: fresh`
(`TestFreshStoreHasNoSetupRow`). A reset that keeps the database and only
clears claim state must also delete `wizard_state` where `wizard = 'setup'`.

## Operating it from the CLI

```
homesteadctl setup status            # origin, the four steps, what is next
homesteadctl setup step domain       # one step's state and detail
homesteadctl setup step domain --provider cloudflare --access tunnel
homesteadctl setup step account --acknowledge  # confirm my password
homesteadctl setup step apps --acknowledge     # also domain, once its tunnel/exposure exists
homesteadctl setup skip sign-in
homesteadctl setup unskip sign-in
```

Resuming is automatic: the dashboard opens on the first pending step after a
reload or a new login, and `setup status` shows the same answer. The CLI
commands change only the walkthrough's record; credentials and plans use their
own commands (`tls issuance credentials`, `sso ...`, tunnel and exposure
commands, `apps install`).
