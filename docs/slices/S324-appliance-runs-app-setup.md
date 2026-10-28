# S324 — The appliance runs the setup approved with an install

## Goal
ADR-048 §1–§3, for catalog apps. The address, public address, sign-in
and people the owner approves in the install review run on the
appliance once the install succeeds. Before S324 the wizard's browser
tab chained them, so closing the tab left them half done.

## Scope
- Install: `POST /api/v1/catalog/{name}/install` takes an optional
  `setup`:
  - `public` {hostname, route, digest};
  - `sign_in` {digest};
  - `people` [ids].

  The approvals are recorded in SQLite (migration 54, "app setup", table
  `app_setup`, one row per app).
  - Sign-in needs the kept Authentik token. Without it, the sign-in and
    people steps are recorded as skipped, with a reason that says how to
    set sign-in up later.
  - People are only part of a setup that sets up sign-in.
- `app.setup` operation: the install's last step starts it. Steps run in
  this order, each only when present:
  1. address on your network: the install plan's non-public routes,
     merged into the edge route table through the existing reconcile;
  2. public address, by the approved digest (`app.public.add`);
  3. sign-in with Authentik, by the approved digest (`app.sign-in`);
  4. people who can sign in, each granted as a user (`people.grant`).

  Each step plans again and compares its digest first.
  - If the plan changed, the step is marked changed. If a step fails, it
    is marked failed.
  - Either way the setup stops there, records the step and the reason,
    and runs nothing after it.
  - Each step's own operation and audit entries are unchanged.
  - An `app.setup` entry names the install operation and the approved
    digests.
- Reading, resuming and skipping:
  - `GET .../setup` returns the setup. For a stopped setup it includes
    the stopped step's current plan and digest.
  - `POST .../setup/resume {digest, token?}` runs that step and the rest.
    It answers 409 when the digest is stale. The token is used once and
    is never stored, logged or audited.
  - `POST .../setup/skip` ends a stopped setup.
  - Resume and skip are audited as `app.setup.resume` and
    `app.setup.skip`.
  - The catalog list carries `setup: running|stopped`.
- CLI: `homesteadctl apps setup status|resume|skip <pkg>`.
  `apps install --public --approve-public D` now sends the approval with
  the install and follows the setup. It no longer publishes from the CLI.
- Dashboard:
  - The wizard sends the approvals with the install and asks for no
    token. Its Done step follows the setup panel.
  - The catalog card shows a running or stopped setup, with each step's
    state and Review, Approve and resume, and Skip the rest.
  - Home lists Doctor's new `app-setup:<app>` check, with its own title
    and a link to the catalog.
- Doctor: one warn check, in the Apps group, for each installed app
  whose setup is running or stopped.

## Rules
AGENTS.md slice rules apply. ADR-048 is accepted and committed with this
slice. PARITY gains the three setup routes.

## Acceptance criteria
- Go tests cover:
  - steps running in order with no client attached;
  - a changed plan stopping the setup;
  - a failed step stopping the setup;
  - a resume needing a fresh digest;
  - a skip;
  - status and the catalog flag;
  - a failed install forgetting the setup;
  - an interrupted run reading as stopped;
  - the approval rules, including sign-in without a kept token.
- CLI tests cover `apps setup` and `apps install --public`.
- Dashboard specs cover the setup panel, the wizard's Done step, the
  catalog card and Home.
- The parity test, and all Go, Python and dashboard checks, pass.

## Live check
On VM 2, install an app with a public address approved:
1. Close the tab during the install.
2. On return, the card shows the setup done, and the audit shows
   `app.setup` with the digests.

Change the tunnel between the review and the install. The setup should
stop at the public address, and Review, Approve and resume should finish
it.
