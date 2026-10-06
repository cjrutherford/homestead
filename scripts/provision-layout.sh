#!/usr/bin/env bash
# Provision the Homestead filesystem layout (S012). Root-gated, idempotent.
# Mirrors internal/layout Dirs — tests/test_s012_layout.py cross-checks both.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
	echo "error: run as root (e.g. sudo make install)" >&2
	exit 1
fi

provision() {
	# $1 = path, $2 = mode
	install -d -o root -g root -m "$2" "$1"
}

provision /etc/homestead 0755
provision /var/lib/homestead 0700
provision /var/log/homestead 0755
provision /run/homestead 0755
provision /opt/homestead/stacks 0755

echo "homestead filesystem layout provisioned"
