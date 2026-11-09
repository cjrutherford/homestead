#!/usr/bin/env bash
# Bootstrap a clean Debian/Ubuntu machine into an installed native
# Homestead kernel (S220): minimal OS dependencies, the Go toolchain
# the module requires, Docker Engine and cloudflared from their vendors'
# pinned-key apt repositories (S308, ADR-042), then the systemd install.
# Runs from a repository checkout: ./scripts/bootstrap.sh (as root).
#   --no-docker       Docker is managed by the owner (it must be present)
#   --no-cloudflared  skip the tunnel connector (home network only)
# Idempotent: safe to re-run; present dependencies are verified, not
# reinstalled, and install-systemd.sh converges the rest.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"

WANT_DOCKER=1
WANT_CLOUDFLARED=1
for arg in "$@"; do
	case "$arg" in
	--no-docker) WANT_DOCKER=0 ;;
	--no-cloudflared) WANT_CLOUDFLARED=0 ;;
	*) echo "error: unknown argument: $arg (use --no-docker, --no-cloudflared)" >&2; exit 2 ;;
	esac
done

# Vendor apt repository keys, pinned (ADR-042). Override for one run only
# after checking the vendor's published fingerprint for a rotated key.
DOCKER_KEY_FPR="${HOMESTEAD_DOCKER_KEY_FPR:-9DC858229FC7DD38854AE2D88D81803C0EBFCD88}"
CLOUDFLARE_KEY_FPR="${HOMESTEAD_CLOUDFLARE_KEY_FPR:-CC94B39C77AE7342A68B89628A682D308D4E5E73}"

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
# script — never a metapackage sprawl. gpg checks the pinned vendor
# repository keys (ADR-042).
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl nftables age gpg

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

# fetch_key URL DEST FINGERPRINT: download a repository key and install it
# only if its primary key fingerprint is the pinned one.
fetch_key() {
	local url="$1" dest="$2" want="$3" dir got
	dir="$(mktemp -d)"
	if ! curl -fsSL -o "$dir/key" "$url"; then
		rm -rf "$dir"
		echo "error: cannot download $url" >&2
		return 1
	fi
	got="$(gpg --show-keys --with-colons "$dir/key" 2>/dev/null | awk -F: '/^fpr/{print $10; exit}')"
	if [ "$got" != "$want" ]; then
		rm -rf "$dir"
		echo "error: $url has fingerprint '${got:-none}', expected $want; refusing to trust it (ADR-042)" >&2
		return 1
	fi
	install -d -m 0755 "$(dirname "$dest")"
	install -m 0644 "$dir/key" "$dest"
	rm -rf "$dir"
}

# Docker and Cloudflare publish for Debian and Ubuntu releases only.
REPO_DISTRO=""
REPO_CODENAME=""
case "${ID:-}" in
debian | ubuntu)
	REPO_DISTRO="$ID"
	REPO_CODENAME="${VERSION_CODENAME:-}"
	;;
*)
	case " ${ID_LIKE:-} " in
	*" ubuntu "*)
		REPO_DISTRO="ubuntu"
		REPO_CODENAME="${UBUNTU_CODENAME:-}"
		;;
	esac
	;;
esac
ARCH="$(dpkg --print-architecture)"

need_vendor_repo() {
	if [ -z "$REPO_DISTRO" ] || [ -z "$REPO_CODENAME" ]; then
		echo "error: no vendor packages for '${ID:-unknown}'; install $1 yourself, then re-run with $2" >&2
		exit 1
	fi
	case "$ARCH" in
	amd64 | arm64) ;;
	*) echo "error: no vendor packages for architecture $ARCH; install $1 yourself, then re-run with $2" >&2; exit 1 ;;
	esac
}

if [ "$WANT_DOCKER" -eq 1 ]; then
	if command -v docker >/dev/null && docker compose version >/dev/null 2>&1; then
		echo "docker present: $(docker --version)"
	elif command -v docker >/dev/null; then
		echo "error: docker is installed without Compose v2. Install the matching" >&2
		echo "  compose package (Debian: docker-compose; Docker repo: docker-compose-plugin)," >&2
		echo "  or remove docker.io and re-run to get Docker's own packages" >&2
		exit 1
	else
		need_vendor_repo "Docker Engine with Compose v2" "--no-docker"
		fetch_key "https://download.docker.com/linux/$REPO_DISTRO/gpg" /etc/apt/keyrings/docker.asc "$DOCKER_KEY_FPR"
		echo "deb [arch=$ARCH signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$REPO_DISTRO $REPO_CODENAME stable" \
			>/etc/apt/sources.list.d/docker.list
		apt-get update
		apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
		echo "docker installed: $(docker --version)"
	fi
	systemctl enable --now docker.service
	docker compose version >/dev/null || { echo "error: docker compose does not work" >&2; exit 1; }
else
	command -v docker >/dev/null || echo "warning: --no-docker and no docker found; apps and the edge need Docker" >&2
fi

if [ "$WANT_CLOUDFLARED" -eq 1 ]; then
	if command -v cloudflared >/dev/null; then
		echo "cloudflared present: $(cloudflared --version 2>&1 | head -n1)"
	else
		need_vendor_repo "cloudflared" "--no-cloudflared"
		fetch_key https://pkg.cloudflare.com/cloudflare-public-v2.gpg /usr/share/keyrings/cloudflare-public-v2.gpg "$CLOUDFLARE_KEY_FPR"
		echo "deb [signed-by=/usr/share/keyrings/cloudflare-public-v2.gpg] https://pkg.cloudflare.com/cloudflared any main" \
			>/etc/apt/sources.list.d/cloudflared.list
		apt-get update
		apt-get install -y cloudflared
		# The tunnel runs only as homestead-tunnel after an approved plan;
		# the package enables no service of its own.
		echo "cloudflared installed: $(cloudflared --version 2>&1 | head -n1)"
	fi
fi

"$REPO/scripts/install-systemd.sh"

echo "homestead kernel bootstrapped"
