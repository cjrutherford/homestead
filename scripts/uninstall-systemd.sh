#!/usr/bin/env bash
# Remove the homestead-server systemd service (S011). Idempotent.
set -euo pipefail

BIN_DST="/usr/local/bin/homestead-server"
CTL_DST="/usr/local/bin/homesteadctl"
UNIT_DST="/etc/systemd/system/homestead-server.service"

if [ "$(id -u)" -ne 0 ]; then
	echo "error: run as root (e.g. sudo make uninstall)" >&2
	exit 1
fi

if command -v systemctl >/dev/null; then
	systemctl disable --now homestead-server.service 2>/dev/null || true
	rm -f "$UNIT_DST"
	systemctl daemon-reload
fi
rm -f "$BIN_DST" "$CTL_DST"
echo "homestead-server uninstalled"
