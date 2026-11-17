# S383 — Separate container ports from host port requirements

## Goal
Allow proxy-routed catalog apps to install when their container's listening port is occupied on the host. Keep Caddy pointed at the app's actual container port; do not publish a host port.

## Dependencies and scope
S152–S154, S341, ADR-005, ADR-006 and ADR-044. Add optional per-service container port metadata and use it as the dashboard's editable route-target default. Move the 14 curated packages' incorrectly declared internal ports out of host requirements. Preserve genuine host-port requirement checks and the existing explicit container-port API and CLI contract. No runtime, security, or tunnel architecture changes.

## Acceptance
- A curated package remains installable when its container port is already bound on the host; its route targets the original container port and its generated Compose publishes no host port.
- Other genuine host-port requirements still fail when occupied, and container ports are validated.
- The install wizard defaults to the exposed service's declared container port and can still edit the target; CLI and API retain the same request format.
- Tests cover catalog grading and planning, metadata validation, and the wizard; parity and deferred documentation explain the split.
- A future package that genuinely needs a published host port needs a separate planned, validated override and mapping flow. No current curated package declares one.
