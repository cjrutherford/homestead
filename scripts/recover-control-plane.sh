#!/usr/bin/env bash
# Recover the Homestead control plane without destroying workloads
# (S230): stop the daemon (containers keep running — the daemon never
# parents them), snapshot operational state, reinstall binary and unit
# through the tested install path, restart, then prove health plus an
# unchanged workload set. Runs from a repository checkout as root.
# Idempotent and safe to re-run; every phase asserts and fails loudly.
#
# A missing database is not control-plane recovery — it is disaster
# recovery (S234): this script refuses and says so instead of
# booting an AMNESIAC daemon over live workloads.
#
# Optional:
#   HOMESTEAD_HEALTH_URL  health endpoint (default http://127.0.0.1:8080/healthz)
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
: "${HOMESTEAD_HEALTH_URL:=http://127.0.0.1:8080/healthz}"
DB_FILE="/var/lib/homestead/homestead.db"

if [ "$(id -u)" -ne 0 ]; then
	echo "error: run as root (e.g. sudo ./scripts/recover-control-plane.sh)" >&2
	exit 1
fi

phase() { echo "=== recovery: $1"; }
fail() { echo "recovery FAILED at $1: $2" >&2; exit 1; }

phase "preconditions"
if [ ! -f /etc/os-release ]; then
	fail preconditions "/etc/os-release missing; refusing to guess the distribution"
fi
. /etc/os-release
case "${ID:-} ${ID_LIKE:-}" in
	*debian* | *ubuntu*) ;;
	*) fail preconditions "unsupported distribution '${ID:-unknown}'" ;;
esac
for tool in systemctl docker curl go; do
	command -v "$tool" >/dev/null || fail preconditions "missing tool: $tool"
done
test -f "$DB_FILE" || fail preconditions "no operational database at $DB_FILE: this is disaster recovery (S234), not control-plane recovery"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

phase "workload inventory"
docker ps -q | sort >"$TMP/before" || fail inventory "cannot list containers"
test -s "$TMP/before" || echo "warning: no running containers to protect" >&2

phase "stop daemon"
systemctl stop homestead-server.service 2>/dev/null || echo "daemon already stopped"

phase "snapshot state"
SNAPSHOT="/var/lib/homestead/recovery-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$SNAPSHOT"
chmod 0700 "$SNAPSHOT"
cp -a "$DB_FILE"* "$SNAPSHOT/" || fail snapshot "cannot copy operational database"
echo "state snapshotted to $SNAPSHOT"

phase "reinstall"
"$REPO/scripts/install-systemd.sh" || fail reinstall "install path failed"

phase "health"
curl -fsSL --retry 12 --retry-delay 5 --retry-connrefused "$HOMESTEAD_HEALTH_URL" >/dev/null \
	|| fail health "daemon unhealthy at $HOMESTEAD_HEALTH_URL"

phase "workload check"
docker ps -q | sort >"$TMP/after" || fail workloads "cannot list containers"
if ! diff "$TMP/before" "$TMP/after" >/dev/null; then
	echo "workload set changed during recovery:" >&2
	diff "$TMP/before" "$TMP/after" >&2 || true
	fail workloads "containers disappeared; state snapshot at $SNAPSHOT"
fi

echo "control plane recovered; workloads intact; state snapshot at $SNAPSHOT"
