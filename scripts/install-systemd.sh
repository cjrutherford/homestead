#!/usr/bin/env bash
# Install homestead-server as a native systemd service (S011; see ADR-001).
# Idempotent: safe to re-run to pick up a rebuilt binary.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
UNIT_SRC="$REPO/systemd/homestead-server.service"
BIN_DST="/usr/local/bin/homestead-server"
CTL_DST="/usr/local/bin/homesteadctl"
UNIT_DST="/etc/systemd/system/homestead-server.service"
CATALOG_SRC="$REPO/catalog"
CATALOG_DST="/usr/local/share/homestead/catalog"

if [ "$(id -u)" -ne 0 ]; then
	echo "error: run as root (e.g. sudo make install)" >&2
	exit 1
fi
command -v go >/dev/null || { echo "error: go toolchain not found on PATH" >&2; exit 1; }
command -v systemctl >/dev/null || { echo "error: systemctl not found" >&2; exit 1; }
test -f "$UNIT_SRC" || { echo "error: unit file missing: $UNIT_SRC" >&2; exit 1; }

cd "$REPO"
"$REPO/scripts/provision-layout.sh"
# Group allowed on the craft file socket besides root (S254, ADR-032).
getent group homestead-craft >/dev/null || groupadd --system homestead-craft
# A built dashboard (make dashboard-build dashboard-embed) travels inside
# the binary; without one the daemon stays API-only (S031).
build_tags=()
if [ -f "$REPO/internal/dashboard/dist/index.html" ]; then
	build_tags=(-tags embeddash)
fi
go build "${build_tags[@]}" -o "$BIN_DST" ./cmd/homestead-server
# The CLI shares the daemon's API version, so it is rebuilt with it:
# a stale homesteadctl beside a new daemon misreads changed responses.
go build -o "$CTL_DST" ./cmd/homesteadctl
# The packaged catalog travels with the binary: curated manifests are
# code, versioned and reviewed with it (S160). Replaced wholesale so
# removed packages never linger beside new ones.
rm -rf "$CATALOG_DST"
mkdir -p "$(dirname "$CATALOG_DST")"
cp -r "$CATALOG_SRC" "$CATALOG_DST"
# The shared craft Dockerfile and build context (S279) ship in the catalog
# directory; a signed craft manifest records the digest of this tree, so a
# changed file refuses to build until it is re-signed.
mkdir -p "$CATALOG_DST/craft"
rm -rf "$CATALOG_DST/craft/context"
cp -r "$REPO/images/craft" "$CATALOG_DST/craft/context"
install -m 0644 "$UNIT_SRC" "$UNIT_DST"
# The privileged apply unit (ADR-027) ships disabled: the daemon starts it
# on demand when a verified platform update is applied. Only this unit may
# write /usr/local/bin.
install -m 0644 "$REPO/systemd/homestead-apply.service" /etc/systemd/system/homestead-apply.service
# The tunnel connector unit ships disabled: tunnel setup enables it once
# a tunnel exists (S246). Installing the file needs privilege; enabling
# stays an explicit setup-time act, never a side effect of install.
install -m 0644 "$REPO/systemd/homestead-tunnel.service" /etc/systemd/system/homestead-tunnel.service
systemctl daemon-reload
systemctl enable homestead-server.service
systemctl restart homestead-server.service
systemctl --no-pager --lines=5 status homestead-server.service
