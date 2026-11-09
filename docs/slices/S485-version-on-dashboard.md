# S485 - Version on the dashboard

## Goal and dependencies
The Status page shows the daemon version from `GET /version` (G5), the same answer `homesteadctl version` prints. Depends on S483.

## What changed
- New `ApiService.daemonVersion()` wrapper for `GET /version`. No daemon, CLI or route change.
- The Appliance panel on `/admin/status` reads it; `/healthz` and `/api/v1/identity` stay as a fallback if the call fails.
- `docs/PARITY.md` row for `/version` now names `daemonVersion`.

## Verification
Dashboard spec in `status.spec.ts` (the shown version comes from `/version`, not health), `tests/test_s485_version_on_dashboard.py`, checklist row `END-PARITY-5`, and a live check on VM 2.
