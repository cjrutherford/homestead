# S316: The Doctor screen

## Goal
The owner wants one place that says what is wrong with the appliance, why,
and how to fix it. Today `GET /api/v1/doctor` and `homesteadctl doctor`
report hardware, storage, layout, the local CA, certificate renewals, app
data placement and mDNS checks. The dashboard only uses them to fill
"Needs you" on Status. None of the failures from the 2026-10-26 acceptance
run is checked: the tunnel name clash, a public name failing TLS, Authentik
unreachable, an app that does not answer or did not finish setting up.

After S316 there is a Doctor page that shows every check, grouped and
explained, including new live probes. Doctor never acts (owner decision,
2026-10-26). It explains each problem and links to the page or CLI command
that fixes it.

Depends on S315.

## Scope
- **Groups.** Every check carries a group:
  - System: OS, memory, CPU, layout directories.
  - Storage: root disk, mounts, free space on each registered mount, stale
    mounts, app data placement.
  - Network: gateway, resolvers, local names, the `.lan` resolver, mDNS.
  - Certificates: the local CA, local leaves, renewals, edge certificate
    coverage for public names (S312).
  - Tunnel: the unit is active and connected, with the number of
    connections from the cloudflared metrics endpoint on 127.0.0.1;
    whether the tunnel exists in Cloudflare when a token is kept; and DNS
    records.
  - Sign-in: Authentik answers at its issuer (`<server>/apps/auth/`,
    S315); the dashboard sign-in plan is applied; apps open to every
    Authentik user (S314).
  - Apps: per installed app, the containers are running and healthy, the
    app answers on each of its addresses (the S313 reach check, local and
    public, including TLS), and first run is finished where that can be
    detected (Nextcloud `occ status`).
- **Check shape.** Extend `doctor.Check` with:
  - `group`;
  - `title`, a plain-words name;
  - `why`, one sentence on what it means;
  - `fix`, with `text`, an optional `link` (a dashboard route) and an
    optional `command` (the `homesteadctl` command).

  Existing checks get these fields. The `name`, `status` and `detail`
  fields stay, for compatibility with the CLI and the Status page.
- **Live probes.**
  - Bounded: each probe has a short timeout and the whole report has a
    budget, so a slow probe gives `unknown`, never a hang.
  - Read-only.
  - Probes run on request. `GET /api/v1/doctor` stays cheap, and the live
    probes run with `?live=1`.
  - Never put secrets in a detail.
- **Dashboard Doctor page** (`/doctor`, in the System nav):
  - A masthead with the overall state as its lead ("2 problems need you",
    "Everything checked is fine").
  - Problems first, as lit fields with the fix text, the link and a
    copyable command.
  - Then each group as a field with its checks as lamp rows, with ok rows
    collapsed by default.
  - **Check again** runs the live probes, shows when they last ran, and
    shows a skeleton while running.
  - Lamplight language. Add the page to the lamplight enforcement spec's
    route list.
  - Status's "Needs you" and its fallback fix link to `/doctor`.
- **CLI:**
  - `homesteadctl doctor` gains `--live`, prints the group headings, and
    prints each problem's fix text and command.
  - The existing JSON output keeps its fields and gains the new ones.
- **Docs:** INSTALL.md troubleshooting starts with "open Doctor", and
  PARITY is updated for the doctor row and the new page.

## Out of scope
- Doctor taking any action.
- A support bundle.
- Scheduled background probing.

Record any of these in DEFERRED if useful.

## Rules
- AGENTS.md slice rules apply.
- No ADR is needed: the change is read-only and adds no new exposure.

## Acceptance Criteria
- Go tests cover each new probe (healthy, failing, timeout leading to
  unknown), the grouping and the fix fields, and that the live budget is
  respected.
- CLI tests cover `--live` and the grouped output.
- Dashboard specs cover the page states (all fine, problems, probe running,
  probe failed) and the Status link.
- Go, Python and dashboard tests pass; build and embed succeed; PARITY is
  updated.
- Screenshots of Doctor in dark and light, desktop and 375px, with and
  without problems, reviewed by looking at them.
- A live check on VM 2 shows real results: tunnel connected, the app
  answers, a deliberately stopped container reported with its fix.
