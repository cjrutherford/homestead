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

The TCP listener is wrapped by the operator session boundary (ADR-020):
every route is classified public, claim-window, or session in code and the
middleware enforces it (since S257): unauthenticated requests to non-public
routes get 401, pre-claim access gets 403, and an unclassified route is
refused. The root-only Unix socket is exempt; its file mode is its boundary.
Sessions share one store with two transports (S256): an HttpOnly,
SameSite=Strict cookie plus `X-CSRF-Token` for the browser, and
`Authorization: Bearer` for the CLI; 12h idle / 7d absolute lifetime.

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
