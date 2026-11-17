# S463 - Docker address pools sized for the full catalog

## Goal and dependencies
EV-THREE-D-1 (Everything acceptance run on VM 2). Docker's built-in pool gives each compose network a /16 or /20 and runs dry at about 31 networks: installs then fail "could not find an available, non-overlapping IPv4 address pool". The catalog has 46 apps. Depends on S011 (installer) and S044/S316 (Doctor).

## Decision (no ADR needed)
The least invasive fix is Docker's own `default-address-pools` in `/etc/docker/daemon.json`, set by the installer. Docker Compose stays usable without Homestead: the key is a plain Docker setting, generated compose files are untouched, and the owner can change or remove it.

## What changed
- `internal/dockerpools`: merges one pool (`10.200.0.0/16`, size 24, 256 networks) into daemon.json. First candidate of `10.200`..`10.203` that overlaps no host interface network wins. Other keys are kept; a file that already has `default-address-pools` (the owner's) and a file that is not valid JSON are left alone.
- `homestead-server docker-pools` (root, run by `scripts/install-systemd.sh`) prints `changed` or `unchanged`; the installer restarts Docker only on `changed` (a pool is read at daemon start; running apps return by restart policy; existing networks keep their subnets). A failure only warns.
- Doctor check `docker-address-pools` (System group): warns when the pools are missing or hold fewer than 64 networks.
- VM 2's hand-made workaround is exactly what the product now writes.
- No API route, CLI command or dashboard surface changed; `docs/PARITY.md` is unchanged.

## Tests
`internal/dockerpools/dockerpools_test.go`, `internal/doctor/doctor_test.go` (`TestCheckDockerPools`), `tests/test_s463_docker_pools.py`.
