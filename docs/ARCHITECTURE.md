# Architecture

## Core components
- `homestead-server`: native static Go daemon, systemd-managed, root-owned.
- `homesteadctl`: standalone Go CLI using the same API.
- Angular dashboard: compiled static assets served by the daemon in production.
- SQLite: operations, sessions, onboarding, audits, wizard state.
- YAML: customer-readable desired configuration and generated artifacts.
- Docker Engine + Compose v2: application runtime.
- Caddy: application reverse proxy / HTTP edge.
- AdGuard Home: initial local DNS provider.
- Headscale/Tailscale: private overlay providers.
- Dockge: expert Compose interface.

## Control boundary
The browser never receives Docker socket access. The Go daemon owns host-level mutation.

## Desired/generated/observed
Homestead separates:
- desired state: what the owner requested;
- generated state: concrete config Homestead produced;
- observed state: what is actually running.

## Paths
- `/usr/local/bin/homestead-server`
- `/usr/local/bin/homesteadctl`
- `/etc/homestead/`
- `/var/lib/homestead/`
- `/var/log/homestead/`
- `/run/homestead/`
- `/opt/homestead/stacks/`
