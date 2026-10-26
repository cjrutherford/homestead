# S313 — App install sets up the local address and the public address

## Goal
The owner wants installing an app to leave it reachable at both of its
addresses, without a second trip to Manage:

- the **local** address: `<app>.lan` and `<app>.local`;
- the **public** address through the tunnel: `<app>.<domain>` (the owner's domain, e.g. `nextcloud.homestead.<zone>`).

Today the install wizard only shows the local names. The public name is a
separate Manage action (S290). Nothing checks that either address actually
answers.

Depends on S312: the depth rule and Nextcloud's automatic first run.

## Scope

### Address step (install wizard and CLI)
- The address step shows both addresses.
- **Local:**
  - `<app>.lan` and `<app>.local` are shown as now.
  - If the appliance `.lan` resolver (S295) is off, the step says that
    `.lan` needs the resolver or a router DNS entry and links to Names.
    The `.local` address is shown as the one that works without setup.
- **Public:** when a tunnel is set up, the step offers **Also publish at**
  with the proposed name `<app>.<domain>`, editable, with the S312 coverage warning when no certificate covers it (the
  owner may still choose it). It is on by default for apps that set up Authentik sign-in in the
  same install. It is off by default otherwise, with the existing warning
  that the app's own sign-in protects it. When no tunnel exists, the step
  says so and links to the Tunnel page.

### Plan and approval
Public exposure stays planned, previewed and approved (ADR-041, AGENTS.md).
- The wizard's review step shows the install permit and, when publishing,
  the **public address permit** (the S290 plan). The owner approves each
  one: two stamps, or one approval covering both digests if the existing
  API allows it without a new route.
- After the install operation succeeds, the wizard applies the public
  address by its approved digest. Then, where chosen, it applies sign-in.
  The progress shows each phase.
- Do not weaken digest binding. If the public plan's digest no longer
  matches after install, re-plan and ask again.
- The CLI has parity: `homesteadctl apps install <app> --public [NAME]`
  plans both, prints both digests and requires `--approve-public <digest>`
  (or the existing approve pattern). Follow how the CLI already chains
  plan and apply, and update `docs/PARITY.md`.

### Making the addresses actually work
- **Nextcloud trusted domains:** `<app>.lan`, `<app>.local` and the public
  name are all trusted. Nextcloud is configured to generate https URLs
  behind the tunnel (`overwriteprotocol`, `overwrite.cli.url` or the
  image's env). A public name added or removed later updates trusted
  domains through the existing occ path.
- Check the same for every catalog app that has an allowed-hosts style
  setting (Immich, Jellyfin, Authentik). Fix what is needed, or record it
  in `docs/DEFERRED.md` with the reason.
- **Reachability check:** the final wizard step and the CLI output check
  each address from the appliance.
  - For the local address, the edge answers for the `.lan` and `.local`
    host.
  - For the public address, an HTTPS request to the public name succeeds,
    with the TLS handshake included.
  - Each result is shown as a lamp with the word "answers" or "does not
    answer yet", plus the reason and the fix: for example, "your computer
    cannot resolve nextcloud.lan: turn on the resolver or add a router DNS
    entry", or the S312 certificate message.
  - The check is advisory and never fails the install.

## Rules
AGENTS.md slice rules apply. Follow the S310/S311 lamplight language and
the lamplight enforcement spec. Update `docs/INSTALL.md` D5 so that
publishing is part of the install, and the Manage panel stays the place
to change it later.

## Acceptance Criteria
- Dashboard specs cover the address step with a tunnel and without one,
  resolver on and off, the public toggle defaults, and the two permits
  with apply chaining.
- Go and CLI tests cover `--public`, digest binding, Nextcloud trusted
  domains and overwrite settings for local and public names, and the
  reachability check results.
- Go, Python and dashboard tests pass; build and embed succeed;
  `docs/PARITY.md` is updated.
- Screenshots of the address, review and done steps in dark, desktop and
  375px, against the mock, reviewed by looking at them.
