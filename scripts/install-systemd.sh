#!/usr/bin/env bash
# Install homestead-server as a native systemd service (S011; see ADR-001).
# Idempotent: safe to re-run to pick up a rebuilt binary.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
UNIT_SRC="$REPO/systemd/homestead-server.service"
BIN_DST="/usr/local/bin/homestead-server"
UNIT_DST="/etc/systemd/system/homestead-server.service"

if [ "$(id -u)" -ne 0 ]; then
	echo "error: run as root (e.g. sudo make install)" >&2
	exit 1
fi
command -v go >/dev/null || { echo "error: go toolchain not found on PATH" >&2; exit 1; }
command -v systemctl >/dev/null || { echo "error: systemctl not found" >&2; exit 1; }
test -f "$UNIT_SRC" || { echo "error: unit file missing: $UNIT_SRC" >&2; exit 1; }

cd "$REPO"
"$REPO/scripts/provision-layout.sh"
go build -o "$BIN_DST" ./cmd/homestead-server
install -m 0644 "$UNIT_SRC" "$UNIT_DST"
systemctl daemon-reload
systemctl enable homestead-server.service
systemctl restart homestead-server.service
systemctl --no-pager --lines=5 status homestead-server.service
