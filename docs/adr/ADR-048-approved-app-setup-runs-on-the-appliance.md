# Approved App Setup Runs on the Appliance; Tunnel Setup Issues the Public Certificate

## Status
Accepted

The owner chose each decision below on 2026-10-27, after the second VM 2
acceptance run, and approved Let's Encrypt certificates for the public
dashboard name. Amends ADR-039 §3 and S313 (who runs the steps after an
install), ADR-046 §1 (what the tunnel plan carries), and ADR-047 §1 and
§3 (how the certificate is issued and how applied apps move to the public
issuer).

## Context
Found on VM 2 (2026-10-27), after a factory reset at S323:
- The install wizard runs the steps after an install in the browser:
  1. the address on the network;
  2. the public address the owner approved with the install;
  3. sign-in with Authentik, reviewed with the install.
- "Back to the app list" can be clicked at any time, and closing the
  wizard or the tab drops whatever has not run yet:
  - Nextcloud's publish took about ten seconds, and its sign-in never
    started;
  - Immich's address and publish ran only when the wizard was reopened,
    and the reopened wizard had no sign-in plan to apply.
- The appliance logged nothing: from its side, the sign-in was never
  asked for. Rebuilt from a copy of the VM's database, the plans matched
  what the wizard would have sent.
- The People page showed only Jellyfin's groups, because an app's groups
  are created by its sign-in step.
- With no certificate for the public dashboard name, every app kept the
  local issuer (ADR-047 §3's waiting state). The Immich mobile app,
  pointed at Immich's public name, was sent to `homestead.lan`. The plan
  did say so, as one warning among several, and the owner missed it.
- Craft apps go through their own install path. Its steps differ from
  the catalog wizard's, and a craft app could still end up on a name of
  its own (Lightcraft).

## Decision
### 1. The appliance runs what the owner approved with the install
The install request carries the digests the owner approved in the
wizard's review:
- the public address plan;
- the sign-in plan.

They are recorded with the install. Once the install succeeds, the
daemon runs the steps in order, as one setup operation for the app:
1. add the app's address on the network;
2. publish it by the approved public digest;
3. apply sign-in by the approved sign-in digest;
4. grant the people chosen in the wizard.

A step whose digest is absent is not part of the setup. Closing the
wizard, the tab or the browser changes nothing. The wizard, the app's
card, Home and Doctor only show the setup's progress.

Each step is audited as it is today (`app.public.add`, `app.sign-in`
and the rest), with an `app.setup` entry that names the install and the
approved digests.

### 2. Changed plans and failures stop the setup
Before each step, the daemon plans it again and compares the plan with
the approved digest:
- **Plan changed:** the step does not run, and neither does any step
  after it. Nothing is published or changed without an approval of the
  plan as it now stands.
- **Step failed:** the steps after it do not run.

The setup records where it stopped and why.

The owner resumes from the app's card:
- the card shows the step's current plan;
- approving it runs that step and the rest of the setup;
- the owner may instead skip the step, which ends the setup there.

### 3. Where unfinished setup shows
A running or stopped setup appears:
- on the app's card in the catalog, with each step's state and
  Review/Resume;
- on Home, as an item that needs the owner's attention until the setup
  finishes or is skipped;
- in Doctor, as a check that names each app whose approved setup did
  not finish, with the way to resume it.

### 4. Craft apps use the same setup
A craft app's install goes through the same wizard review and the same
appliance-run setup. ADR-045 §1 already puts every craft app at
`/apps/<name>/`; an install that creates a route of its own name is a
defect and is fixed with this decision. Linking a craft app to Nextcloud
becomes automatic and is settled in its own slice.

### 5. Tunnel setup issues the public certificate
When the tunnel plan publishes the dashboard, it also plans the
Let's Encrypt certificate for the public dashboard name (ADR-047 §3):
- DNS-01, through the Cloudflare token already given in setup;
- automatic renewal.

The certificate's issuance plan is part of the tunnel plan's preview and
is bound into its digest. Approving the tunnel plan does three things at
once:
- approves the certificate;
- agrees to the CA's terms, which the plan shows with a checkbox that
  must be ticked;
- confirms the production CA.

The tunnel operation gains a `certificate` step after the connector is
up and before ADR-046's `sign-in` step. Its outcome:
- issued: the certificate is placed;
- failed: the tunnel stays up, as in ADR-046 §3, and the operation says
  the certificate was not issued and how to retry it from TLS.

If a certificate for the name is already placed, the plan says so and
adds no step.

### 6. Applied apps move to the public issuer with the certificate
The plan that issues the certificate lists every app whose sign-in is
applied and whose issuer would move to the public address (ADR-047 §1).
For each one it shows the issuer change and the new host entry, bound
into the plan's digest. Once the certificate is placed, a final step of
the same operation re-applies each listed app's sign-in with the kept
Authentik API token. Each re-apply is audited as a normal `app.sign-in`.

For this step:
- one app that fails does not stop the others;
- the operation names every app that failed, and each failed app's card
  offers the re-plan.

The same move happens when the certificate is issued from TLS instead
of the tunnel plan. The TLS issuance plan lists the apps and carries
their re-apply in the same way. This also covers an appliance whose
tunnel was set up before this decision: issuing the certificate from
TLS moves its apps in one approval.

Without a kept token, nothing in Authentik can change. The plan then
lists the apps and links each one to its sign-in re-plan, as ADR-046 §4
does.

## Alternatives considered
- **Keep the browser running the steps, and block closing while they
  run:** closing the tab still drops them. Not chosen by the owner.
- **Run the remaining steps even after a failure:** sign-in could be
  applied on names the owner did not get, or without the redirect
  addresses the owner reviewed. Not chosen by the owner.
- **Run a changed plan without asking:** what runs could differ from
  what the owner reviewed. Rejected: public exposure must be planned and
  approved (ADR-037).
- **Issue the certificate as a separate, prompted step:** this is the
  ADR-047 flow the owner missed on VM 2. Not chosen.
- **Ask for each app's issuer move separately:** one approval per app
  for a change the owner already chose (ADR-047 §1). Not chosen.

## Consequences
- The install request and its record gain the approved public and
  sign-in digests and the chosen people. A new `app.setup` operation
  runs the steps; its state outlives the wizard.
- The wizard's done step follows the setup operation instead of driving
  the steps itself.
- The app's card, Home and Doctor gain an unfinished-setup item. These
  are new dashboard surfaces, so PARITY gains matching CLI and API
  entries.
- The tunnel plan gains a certificate section and an issuer-move list,
  both in its digest. The TLS issuance plan gains the same issuer-move
  list.
- Approving the tunnel plan now agrees to Let's Encrypt's terms, and
  Let's Encrypt is contacted during tunnel setup.
- Published apps' Authentik sign-in works off the home network as soon
  as tunnel setup finishes.
- Changes require a superseding ADR.

## Implementing slices
In order, one commit each:
1. S324: the appliance-run app setup (§1–§3), for catalog apps.
2. S325: the tunnel plan issues the public certificate, and applied apps
   move to the public issuer (§5–§6), from the tunnel plan and from TLS.
3. S326: craft apps use the same setup; every craft app is on its path;
   Nextcloud linking is automatic (§4).
4. S327: a "Sign in with Authentik" button on Jellyfin's login page,
   set through Jellyfin's login disclaimer and removed when sign-in is
   turned off. A disclaimer the owner wrote is kept. This is an adapter
   detail under ADR-040, recorded here because the owner chose it in the
   same review.
