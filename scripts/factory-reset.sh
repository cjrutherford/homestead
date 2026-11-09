#!/usr/bin/env bash
# Factory reset (S306): return an appliance to the unclaimed state for a new
# owner. Root-gated; refuses to run without --yes.
#
# Removes only what Homestead owns: its systemd units and binaries, the
# Compose projects under /opt/homestead/stacks (with their volumes), the
# edge proxy and homestead-edge network, and the state, config, log and
# runtime directories. Other Compose projects, volumes and images on the
# host are left alone, and docker images are kept so a reinstall is fast.
#
# Mounted storage is never touched unless named with --wipe-mount PATH; then
# the contents of PATH are deleted (lost+found kept). Nothing is formatted.
#
# With --reinstall, scripts/install-systemd.sh runs afterwards from this
# checkout, leaving the daemon active and unclaimed (ready to claim).
#
# Usage:
#   sudo ./scripts/factory-reset.sh --yes [--wipe-mount PATH]... [--reinstall]
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STACKS="/opt/homestead/stacks"
EDGE_NETWORK="homestead-edge"
EDGE_CONTAINER="homestead-caddy"
UNITS=(homestead-server.service homestead-apply.service homestead-tunnel.service)
DIRS=(/opt/homestead /var/lib/homestead /etc/homestead /var/log/homestead
	/usr/local/share/homestead /run/homestead /run/homestead-craft /run/homestead-dashboard)
BINS=(/usr/local/bin/homestead-server /usr/local/bin/homesteadctl)
GROUPS_OWNED=(homestead-dashboard homestead-craft)

usage() {
	sed -n '2,18p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

confirm=0
reinstall=0
wipe=()
while [ $# -gt 0 ]; do
	case "$1" in
	--yes) confirm=1 ;;
	--reinstall) reinstall=1 ;;
	--wipe-mount)
		[ $# -ge 2 ] || { echo "error: --wipe-mount needs a path" >&2; exit 2; }
		wipe+=("$2")
		shift
		;;
	-h | --help) usage; exit 0 ;;
	*) echo "error: unknown argument: $1" >&2; usage >&2; exit 2 ;;
	esac
	shift
done

if [ "$(id -u)" -ne 0 ]; then
	echo "error: run as root (e.g. sudo ./scripts/factory-reset.sh --yes)" >&2
	exit 1
fi
if [ "$confirm" -ne 1 ]; then
	echo "error: this erases the owner, apps and settings; pass --yes to proceed" >&2
	exit 2
fi

# Validate every wipe target before anything is removed: an absolute, real
# directory that is a mount point and not a system path.
for m in "${wipe[@]}"; do
	case "$m" in
	/*) ;;
	*) echo "error: --wipe-mount needs an absolute path: $m" >&2; exit 2 ;;
	esac
	real="$(realpath -e -- "$m" 2>/dev/null)" || { echo "error: no such directory: $m" >&2; exit 2; }
	case "$real" in
	/ | /bin | /boot | /dev | /etc | /home | /lib* | /opt | /proc | /root | /run | /sbin | /srv | /sys | /tmp | /usr | /var)
		echo "error: refusing to wipe system path: $real" >&2
		exit 2
		;;
	esac
	if ! mountpoint -q -- "$real"; then
		echo "error: not a mount point: $real" >&2
		exit 2
	fi
done

echo "== stopping Homestead services"
for u in "${UNITS[@]}"; do
	systemctl disable --now "$u" >/dev/null 2>&1 || true
done

if command -v docker >/dev/null 2>&1; then
	echo "== removing Homestead stacks"
	if [ -d "$STACKS" ]; then
		for dir in "$STACKS"/*/; do
			[ -d "$dir" ] || continue
			name="$(basename "$dir")"
			if [ -f "$dir/compose.yaml" ] || [ -f "$dir/docker-compose.yml" ]; then
				(cd "$dir" && docker compose down -v --remove-orphans) || true
			fi
			# Containers and volumes left behind by a project whose
			# compose file is gone or broken.
			ids="$(docker ps -aq --filter "label=com.docker.compose.project=$name")"
			[ -z "$ids" ] || docker rm -f $ids >/dev/null || true
			vols="$(docker volume ls -q --filter "label=com.docker.compose.project=$name")"
			[ -z "$vols" ] || docker volume rm $vols >/dev/null || true
		done
	fi
	docker rm -f "$EDGE_CONTAINER" >/dev/null 2>&1 || true
	docker network rm "$EDGE_NETWORK" >/dev/null 2>&1 || true
else
	echo "== docker not found; skipping stacks"
fi

echo "== removing Homestead files"
for u in "${UNITS[@]}"; do
	rm -f "/etc/systemd/system/$u"
done
rm -f "${BINS[@]}"
rm -rf "${DIRS[@]}"
for g in "${GROUPS_OWNED[@]}"; do
	if getent group "$g" >/dev/null; then
		groupdel "$g" || true
	fi
done
systemctl daemon-reload

for m in "${wipe[@]}"; do
	real="$(realpath -e -- "$m")"
	echo "== wiping mount $real"
	find "$real" -mindepth 1 -maxdepth 1 ! -name lost+found -exec rm -rf -- {} +
done

echo "factory reset complete"

if [ "$reinstall" -eq 1 ]; then
	echo "== reinstalling from $REPO"
	"$REPO/scripts/install-systemd.sh"
fi
