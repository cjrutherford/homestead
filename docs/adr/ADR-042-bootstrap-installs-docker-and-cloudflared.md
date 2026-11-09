# Bootstrap Installs Docker and cloudflared

## Status
Accepted

The owner decided this on 2026-10-25, for S308.

This record **amends** the S220 bootstrap scope ("minimal OS packages, never
a metapackage sprawl") and closes the DEFERRED item on connector binary
installation. ADR-016 (the trust ladder) is unchanged: installing software
exposes nothing, and claim still comes first.

## Context
A bare-metal install (S307 guide) needed three manual steps before
`bootstrap.sh`. Two of them were installing Docker Engine and Cloudflare's
`cloudflared`. Every appliance needs Docker before the daemon can start the
edge, and most need `cloudflared` for the tunnel. Fulfilment sets up each
appliance by hand, so manual vendor-repository steps are a place for
mistakes. The S071 `docker.install` operation was never wired to anything.

## Decision
1. `scripts/bootstrap.sh` installs Docker Engine with the Compose plugin
   from Docker's apt repository (`download.docker.com`), and `cloudflared`
   from Cloudflare's (`pkg.cloudflare.com`), before the systemd install.
   It enables and starts `docker`. It never enables a `cloudflared` service:
   `homestead-tunnel` runs the connector only after an approved tunnel plan.
2. **Keys are pinned.** Each repository key is fetched over HTTPS and its
   fingerprint is checked against one pinned in the script before apt trusts
   it:
   - Docker `9DC858229FC7DD38854AE2D88D81803C0EBFCD88`;
   - Cloudflare `CC94B39C77AE7342A68B89628A682D308D4E5E73`.

   A mismatch stops the install. A vendor key rotation is handled by
   updating the pin in a release, or for one run with
   `HOMESTEAD_DOCKER_KEY_FPR` / `HOMESTEAD_CLOUDFLARE_KEY_FPR` after the
   operator checks the vendor's published fingerprint. This matches how the
   Go toolchain is checksum-verified.
3. **What is already there wins.** A working Docker with Compose v2
   (including Debian's `docker.io` + `docker-compose`) is kept, and an
   existing `cloudflared` is kept. A `docker` without Compose v2 stops the
   install with instructions, because mixing distribution and Docker
   packages conflicts.
4. **Opt-outs:** `--no-docker` (Docker managed by the owner; it must still
   be present for apps) and `--no-cloudflared` (home network only, or the
   owner installs it later).
5. Supported targets are what both vendors publish: Debian and Ubuntu
   (`ID`, or `ID_LIKE=ubuntu` with `UBUNTU_CODENAME`), on amd64 and arm64.
   Anything else stops with instructions to install both by hand and re-run
   with the opt-outs.

## Consequences
- `bootstrap.sh` adds `gpg` to its base packages for the fingerprint
  check.
- Two vendor apt repositories become part of the appliance's update path.
  Docker and `cloudflared` are updated with ordinary `apt upgrade`;
  Homestead does not pin their versions.
- Docker Compose remains usable without Homestead: the engine is a plain
  vendor install.
- The S071 `docker.install` operation stays unwired; bootstrap is the one
  install path (`docs/DEFERRED.md`).
