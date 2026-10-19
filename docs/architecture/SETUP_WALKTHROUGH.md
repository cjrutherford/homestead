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
| `account` | the latest recovery identity export is acknowledged (`backups/identity`) | pending; skippable |
| `sign-in` | Authentik sign-in is configured, linked and not dormant (`sso` status) | pending; skippable |
| `domain` | the owner acknowledged it, or a domain is onboarded, a tunnel route exists, or an exposure plan is approved | pending; skippable |
| `apps` | the owner acknowledged it, or any catalog app other than Authentik is installed | pending; skippable |

The walkthrough is complete when every step is done or skipped.

The domain step has a review mode. When a domain wizard selection, an
onboarded domain, a tunnel route or an approved exposure plan already exists,
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
differ in either direction.

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
until the walkthrough is complete, except the pages the walkthrough itself
sends the owner to: `/setup`, `/login`, `/claim`, `/account`, `/sign-in`,
`/tunnel`, `/exposure`, `/tls`, `/catalog/:name/install` and
`/operations/:id`. The guard lets a navigation through when the owner is not
logged in or the status cannot be read, so it can never lock the owner out.

An appliance claimed before this slice is not gated. Schema migration 44
inserts the `setup` wizard row `{"origin":"legacy"}` only when the appliance is
already `CLAIMED` at the moment the migration runs. A new, unclaimed appliance
and a factory-reset one get no row and read `origin: fresh`; so do test
fixtures and the acceptance flow, which create a store and claim afterwards.
Legacy appliances see a "Finish setup" navigation entry while steps are
unfinished; the entry is called "Setup" otherwise, and stays after completion.

## Operating it from the CLI

```
homesteadctl setup status            # origin, the four steps, what is next
homesteadctl setup step domain       # one step's state and detail
homesteadctl setup step domain --provider cloudflare --access tunnel
homesteadctl setup step apps --acknowledge   # domain and apps only
homesteadctl setup skip sign-in
homesteadctl setup unskip sign-in
```

Resuming is automatic: the dashboard opens on the first pending step after a
reload or a new login, and `setup status` shows the same answer. The CLI
commands change only the walkthrough's record; credentials and plans use their
own commands (`tls issuance credentials`, `sso ...`, tunnel and exposure
commands, `apps install`).
