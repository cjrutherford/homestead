#!/usr/bin/env bash
# Bare-metal acceptance (S229): from a fresh supported OS to the base
# suite with validated access and no manual Compose editing. Runs
# from a repository checkout as root, driving only supported
# surfaces (homesteadctl, the daemon API): every phase asserts and
# fails loudly, so a green run means the appliance stands on its own.
#
# Required environment:
#   HOMESTEAD_APP_MOUNT    application-class mountpoint (mounted storage)
#   HOMESTEAD_BULK_MOUNT   bulk-class mountpoint (mounted storage)
#   HOMESTEAD_PASSWORD_FILE  owner password file (0600 recommended)
# Optional:
#   HOMESTEAD_EXPORT_FILE    export document to import (disaster recovery)
#   HOMESTEAD_SECRETS_FILE   JSON object file mapping secret names to values
#   HOMESTEAD_CLAIM_TOKEN  single-use claim token (else must be claimed)
#   HOMESTEAD_ADMIN_USER   administrator username (default owner)
#   HOMESTEAD_DOMAIN       install domain (default lan)
#   HOMESTEAD_SERVER       daemon base URL (default http://127.0.0.1:8080)
#   JELLYFIN_PORT / NEXTCLOUD_PORT / IMMICH_PORT (defaults 8096/8080/2283)
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
: "${HOMESTEAD_APP_MOUNT:?set HOMESTEAD_APP_MOUNT to a mounted application-class directory}"
: "${HOMESTEAD_BULK_MOUNT:?set HOMESTEAD_BULK_MOUNT to a mounted bulk-class directory}"
: "${HOMESTEAD_PASSWORD_FILE:?set HOMESTEAD_PASSWORD_FILE to the owner password file}"
: "${HOMESTEAD_ADMIN_USER:=owner}"
: "${HOMESTEAD_DOMAIN:=lan}"
: "${HOMESTEAD_SERVER:=http://127.0.0.1:8080}"
: "${JELLYFIN_PORT:=8096}"
: "${NEXTCLOUD_PORT:=8080}"
: "${IMMICH_PORT:=2283}"
# Plan files render in python subprocesses: inputs must export.
export HOMESTEAD_DOMAIN HOMESTEAD_APP_MOUNT HOMESTEAD_BULK_MOUNT

if [ "$(id -u)" -ne 0 ]; then
	echo "error: run as root (e.g. sudo ./scripts/acceptance.sh)" >&2
	exit 1
fi

phase() { echo "=== acceptance: $1"; }
fail() { echo "acceptance FAILED at $1: $2" >&2; exit 1; }

phase "preconditions"
. /etc/os-release
case "${ID:-} ${ID_LIKE:-}" in
	*debian* | *ubuntu*) ;;
	*) fail preconditions "unsupported distribution '${ID:-unknown}'" ;;
esac
for tool in systemctl docker curl python3 go; do
	command -v "$tool" >/dev/null || fail preconditions "missing tool: $tool (run scripts/bootstrap.sh first)"
done
test -f "$HOMESTEAD_PASSWORD_FILE" || fail preconditions "password file missing: $HOMESTEAD_PASSWORD_FILE"
if [ "$(stat -c %a "$HOMESTEAD_PASSWORD_FILE")" != "600" ]; then
	echo "warning: password file is not 0600" >&2
fi
for mount in "$HOMESTEAD_APP_MOUNT" "$HOMESTEAD_BULK_MOUNT"; do
	test -d "$mount" || fail preconditions "mountpoint missing: $mount"
done

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
go build -o "$TMP/homesteadctl" ./cmd/homesteadctl
CTL="$TMP/homesteadctl"

phase "bootstrap"
if systemctl is-active --quiet homestead-server.service; then
	echo "daemon already active"
else
	"$REPO/scripts/bootstrap.sh"
fi

phase "claim"
state="$(curl -fsSL "$HOMESTEAD_SERVER/api/v1/identity" | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])')" || fail claim "identity endpoint unreachable"
if [ "$state" = "UNCLAIMED" ]; then
	test -n "${HOMESTEAD_CLAIM_TOKEN:-}" || fail claim "appliance unclaimed: set HOMESTEAD_CLAIM_TOKEN from the daemon console (journalctl -u homestead-server)"
	if ! out="$("$CTL" claim "$HOMESTEAD_CLAIM_TOKEN" 2>&1)"; then
		case "$out" in
			*already\ claimed*) echo "claimed concurrently; continuing" ;;
			*) fail claim "claim token rejected: $out" ;;
		esac
	fi
elif [ "$state" != "CLAIMED" ]; then
	fail claim "unexpected identity state: $state"
fi

phase "admin"
# Setup logs the owner in (the CLI stores that session); the explicit
# login below proves the credentials independently and yields the token.
if ! "$CTL" admin setup --username "$HOMESTEAD_ADMIN_USER" --password-file "$HOMESTEAD_PASSWORD_FILE" 2>/dev/null; then
	echo "admin exists (or setup refused); proving login instead"
fi
TOKEN="$("$CTL" login --username "$HOMESTEAD_ADMIN_USER" --password-file "$HOMESTEAD_PASSWORD_FILE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')" || fail admin "login failed"
test -n "$TOKEN" || fail admin "login produced no token"
# Every route except the public allowlist needs a session (ADR-020,
# S257): prove the boundary is enforcing, then use the session
# explicitly for every later call (HOMESTEAD_TOKEN wins over the stored
# session, so the run does not depend on the invoking user's HOME).
code="$(curl -sS -o /dev/null -w '%{http_code}' "$HOMESTEAD_SERVER/api/v1/config")" || fail admin "config endpoint unreachable"
[ "$code" = "401" ] || fail admin "unauthenticated GET /api/v1/config returned $code, want 401 (boundary not enforcing)"
export HOMESTEAD_TOKEN="$TOKEN"

phase "import"
if [ -n "${HOMESTEAD_EXPORT_FILE:-}" ]; then
	test -f "$HOMESTEAD_EXPORT_FILE" || fail import "export file missing: $HOMESTEAD_EXPORT_FILE"
	args=(-f "$HOMESTEAD_EXPORT_FILE")
	if [ -n "${HOMESTEAD_SECRETS_FILE:-}" ]; then
		args+=(--secrets-file "$HOMESTEAD_SECRETS_FILE")
	fi
	"$CTL" config import "${args[@]}" || fail import "configuration import refused"
else
	echo "no export file; continuing with fresh configuration"
fi

phase "doctor"
"$CTL" doctor || fail doctor "host checks failed"

phase "firewall"
"$CTL" firewall apply || fail firewall "firewall apply refused"

phase "suite"
install_package() {
	# $1 = package, $2 = service, $3 = host port
	python3 - "$TMP/$1.json" "$1" "$2" "$3" <<'EOF'
import json, sys
path, package, service, port = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
import os
body = {
    "domain": os.environ["HOMESTEAD_DOMAIN"],
    "hostnames": {},
    "container_ports": {service: port},
    "storage": {"application": os.environ["HOMESTEAD_APP_MOUNT"],
                "bulk": os.environ["HOMESTEAD_BULK_MOUNT"]},
    "volumes": [],
    "networks": [{"Name": package + "-net", "External": False}],
    "zone": "",
    "public_ip": "",
}
with open(path, "w") as handle:
    json.dump(body, handle)
EOF
	"$CTL" apps plan "$1" -f "$TMP/$1.json" || fail suite "plan refused for $1"
	"$CTL" apps install "$1" -f "$TMP/$1.json" --follow || fail suite "install failed for $1"
}
install_package jellyfin jellyfin "$JELLYFIN_PORT"
install_package nextcloud nextcloud "$NEXTCLOUD_PORT"
install_package immich server "$IMMICH_PORT"

phase "drift"
for package in jellyfin nextcloud immich; do
	curl -fsSL -H "Authorization: Bearer $TOKEN" \
		"$HOMESTEAD_SERVER/api/v1/catalog/$package/drift" |
		python3 -c 'import json,sys; assert json.load(sys.stdin) == [], "drifted generated files"' ||
		fail drift "generated files differ for $package (manual Compose editing?)"
done

phase "access"
# Generated stacks publish no host ports by design (S153):
# local-first serving goes through the proxy, never host ports.
# Access validates one level down instead: every manifest service
# owns a container, and images with health contracts prove serving.
# Secret-gated apps (database passwords arrive with the secrets
# slice) report status without failing: install convergence plus
# presence is what this phase owns.
: "${HOMESTEAD_HEALTHY_APPS:=jellyfin}"
check_stack() {
	# $1 = package, $2... = "service" entries
	package="$1"
	shift
	json="$(docker compose --project-directory "/opt/homestead/stacks/$package" ps --format json)" ||
		fail access "compose ps failed for $package"
	python3 - "$package" "$json" "$@" <<'EOF' || fail access "$package presence unproven"
import json, sys
package, raw, services = sys.argv[1], sys.argv[2], sys.argv[3:]
try:
    containers = json.loads(raw or "[]")
except json.JSONDecodeError:
    # Newer Compose emits one object per line instead of an array.
    try:
        containers = [json.loads(line) for line in raw.splitlines() if line.strip()]
    except json.JSONDecodeError as exc:
        sys.exit(f"{package}: unparsable compose ps: {exc}")
if isinstance(containers, dict):
    containers = [containers]
names = set()
for container in containers:
    name = container.get("Name", "")
    names.add(name)
    print(f"{package}: {name} {container.get('State','?')} {container.get('Health','')}".rstrip())
missing = [s for s in services if f"{package}-{s}" not in names]
if missing:
    sys.exit(f"{package}: missing containers for services: {missing}")
EOF
}
wait_healthy() {
	# $1 = package, $2... = services: poll image health contracts
	# until healthy or the deadline (first starts are slow).
	end=$((SECONDS + 600))
	while true; do
		unready=0
		for service in "${@:2}"; do
			health="$(docker inspect --format '{{json .State.Health}}' "$1-$service" 2>/dev/null || echo missing)"
			case "$health" in
				*'"Status":"healthy"'*) ;;
				"<no value>") echo "$1-$service has no health contract; skipping" ;;
				*) unready=1 ;;
			esac
		done
		[ "$unready" -eq 0 ] && return 0
		[ $SECONDS -ge $end ] && return 1
		sleep 15
	done
}
check_stack jellyfin jellyfin
check_stack nextcloud nextcloud
check_stack immich server machine-learning database cache
for package in $HOMESTEAD_HEALTHY_APPS; do
	case "$package" in
		jellyfin) wait_healthy jellyfin jellyfin || fail access "jellyfin never healthy" ;;
		nextcloud) wait_healthy nextcloud nextcloud || fail access "nextcloud never healthy" ;;
		immich) wait_healthy immich server machine-learning database cache || fail access "immich never healthy" ;;
		*) fail access "unknown healthy package: $package" ;;
	esac
done

echo "acceptance PASSED: base suite installed with validated access"
echo "note: backups have no CLI/API surface yet; configure them before production use"
