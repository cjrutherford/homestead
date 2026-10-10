#!/usr/bin/env bash
# Bootstrap a clean Debian/Ubuntu machine into an installed native
# Homestead kernel (S220): minimal OS dependencies, the Go toolchain
# the module requires, then the existing systemd install.
# Runs from a repository checkout: ./scripts/bootstrap.sh (as root).
# Idempotent: safe to re-run; present dependencies are verified, not
# reinstalled, and install-systemd.sh converges the rest.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"

if [ "$(id -u)" -ne 0 ]; then
	echo "error: run as root (e.g. sudo ./scripts/bootstrap.sh)" >&2
	exit 1
fi

# Clean Debian or Ubuntu only: no other distribution is validated,
# and pretending otherwise strands owners on foreign layouts.
if [ ! -f /etc/os-release ]; then
	echo "error: /etc/os-release missing; refusing to guess the distribution" >&2
	exit 1
fi
# os-release is distribution data, not code.
. /etc/os-release
case "${ID:-} ${ID_LIKE:-}" in
	*debian* | *ubuntu*)
		;;
	*)
		echo "error: unsupported distribution '${ID:-unknown}'; Debian or Ubuntu required" >&2
		exit 1
		;;
esac

command -v systemctl >/dev/null || { echo "error: systemctl not found; systemd is required" >&2; exit 1; }
command -v apt-get >/dev/null || { echo "error: apt-get not found" >&2; exit 1; }

# Minimal OS packages: TLS roots for module downloads, a fetcher for
# the toolchain tarball, and nftables for the firewall the security
# baseline requires (S222) — the package installs no rules by itself.
# Everything else (build, service) comes from Go and the install
# script — never a metapackage sprawl.
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl nftables

# Required toolchain comes from go.mod, overridable for testing new
# releases before the module moves: HOMESTEAD_GO_VERSION=1.26.0.
WANT_GO="${HOMESTEAD_GO_VERSION:-$(awk '/^go /{print $2; exit}' "$REPO/go.mod")}"
test -n "$WANT_GO" || { echo "error: cannot read Go version from $REPO/go.mod" >&2; exit 1; }

have_go() {
	command -v go >/dev/null || return 1
	# go version reports "go version go1.26.0 ...": accept equal or newer.
	current="$(go version | awk '{print $3}')"
	test "${current#go}" = "$WANT_GO" || dpkg --compare-versions "${current#go}" ge "$WANT_GO"
}

if have_go; then
	echo "go toolchain present: $(go version)"
else
	case "$(uname -m)" in
		x86_64) GOARCH=amd64 ;;
		aarch64 | arm64) GOARCH=arm64 ;;
		*)
			echo "error: unsupported architecture '$(uname -m)' for official Go builds" >&2
			exit 1
			;;
	esac
	# Official binaries only (dl.google.com, the go.dev download
	# backend): distribution Go packages lag the toolchain the module
	# requires, and third-party builds have no place in the trust
	# ladder. The checksum endpoint serves the bare hash, hence the
	# reformat before verifying.
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	tarball="go$WANT_GO.linux-$GOARCH.tar.gz"
	curl -fsSL -o "$tmp/$tarball" "https://dl.google.com/go/$tarball"
	curl -fsSL -o "$tmp/$tarball.sha256" "https://dl.google.com/go/$tarball.sha256"
	(cd "$tmp" && echo "$(awk '{print $1}' "$tarball.sha256")  $tarball" | sha256sum -c -)
	rm -rf /usr/local/go
	tar -C /usr/local -xzf "$tmp/$tarball"
	ln -sf /usr/local/go/bin/go /usr/local/bin/go
	hash -r
	echo "go toolchain installed: $(go version)"
fi

"$REPO/scripts/install-systemd.sh"

echo "homestead kernel bootstrapped"
