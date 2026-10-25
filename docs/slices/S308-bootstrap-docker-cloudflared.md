# S308 — Bootstrap installs Docker and cloudflared

## Goal
Remove the two manual vendor-repository steps from a bare-metal install
(S307 guide, A3 and A4): `bootstrap.sh` installs Docker Engine with Compose,
and cloudflared, so fulfilment goes from OS install and mounted disks
straight to `bootstrap.sh`.

## Dependencies
S220 (bootstrap), S246 (tunnel connector), S307 (install guide). ADR-042
records the decision, the pinned keys and the opt-outs.

## Scope
- `scripts/bootstrap.sh`:
  - adds `gpg`;
  - `fetch_key` checks each vendor key's fingerprint against a pin before
    installing it;
  - adds Docker's repository and installs `docker-ce`, `docker-ce-cli`,
    `containerd.io`, `docker-buildx-plugin` and `docker-compose-plugin`,
    then enables Docker;
  - adds Cloudflare's repository and installs `cloudflared`, with no
    service enabled;
  - keeps an existing Docker that has Compose v2, and an existing
    `cloudflared`;
  - refuses a Docker without Compose v2, with instructions;
  - accepts `--no-docker` and `--no-cloudflared`.
- `docs/INSTALL.md`: A3 says there is nothing to do; B3 lists the new
  steps and options; troubleshooting covers the new refusals.
- `docs/DEFERRED.md`: the connector and Docker installation items are
  closed.

Out of scope: the unwired S071 `docker.install` operation (DEFERRED);
pinning Docker or cloudflared versions.

## Acceptance Criteria
- `tests/test_s308_bootstrap_vendor_packages.py`.
- Container trial (2026-10-25), with systemctl, Go and the systemd install
  stubbed:
  - Fresh Debian 13 and Ubuntu 24.04 each install Docker 29.9 (Compose
    v5.6) and cloudflared 2026.10.3, with the repositories `signed-by` the
    pinned keys.
  - A wrong pinned fingerprint stops before any repository is added.
  - Debian `docker.io` without Compose stops with instructions; with
    `docker-compose` it is kept.
  - A re-run is a no-op.
  - `--no-docker --no-cloudflared` skips both.
