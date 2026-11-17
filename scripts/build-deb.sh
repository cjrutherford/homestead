#!/usr/bin/env bash
# Build a local-install Debian package from the signed-key release binaries.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
ARCH="${1:-}"
RELEASE_DIR="${2:-$REPO/dist/release}"
OUTPUT_DIR="${3:-$REPO/dist/deb}"
VERSION="$(tr -d '\n' < "$REPO/VERSION")"
REVISION="${DEB_REVISION:-1}"
MAINTAINER="${DEB_MAINTAINER:-}"

case "$ARCH" in
	amd64 | arm64) ;;
	*) echo "usage: $0 <amd64|arm64> [release-dir] [output-dir]" >&2; exit 2 ;;
esac
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "error: VERSION is not semver" >&2; exit 1; }
[[ "$REVISION" =~ ^[0-9][A-Za-z0-9.+~]*$ ]] || { echo "error: invalid DEB_REVISION" >&2; exit 1; }
[[ "$MAINTAINER" != *$'\n'* && "$MAINTAINER" == *'<'*'>'* ]] || {
	echo "error: set DEB_MAINTAINER to a real name and email, for example 'Name <email@example.org>'" >&2
	exit 1
}
command -v dpkg-deb >/dev/null || { echo "error: dpkg-deb is required" >&2; exit 1; }
command -v python3 >/dev/null || { echo "error: python3 is required" >&2; exit 1; }

SERVER="$RELEASE_DIR/homestead-server-linux-$ARCH"
CTL="$RELEASE_DIR/homesteadctl-linux-$ARCH"
for binary in "$SERVER" "$CTL"; do
	[[ -x "$binary" ]] || { echo "error: release binary missing or not executable: $binary" >&2; exit 1; }
done

tmp="$(mktemp -d "${TMPDIR:-/tmp}/homestead-deb.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
root="$tmp/root"
share="$root/usr/local/share/homestead"
mkdir -p "$root/DEBIAN" "$share/catalog" "$root/usr/lib/systemd/system" "$root/usr/share/doc/homestead"

install -D -m 0755 "$SERVER" "$root/usr/local/bin/homestead-server"
install -D -m 0755 "$CTL" "$root/usr/local/bin/homesteadctl"
cp -a "$REPO/catalog/." "$share/catalog/"
mkdir -p "$share/catalog/craft/context" "$share/catalog/everything/context" "$share/catalog/everything/sources"
cp -a "$REPO/images/craft/." "$share/catalog/craft/context/"
cp -a "$REPO/images/everything/." "$share/catalog/everything/context/"
cp -a "$REPO"/third_party/everything/*.tar.gz "$share/catalog/everything/sources/"
# Source worktrees can have group-writable directories. Package catalog and
# build inputs are immutable package data, so normalize their modes.
find "$share" -type d -exec chmod 0755 {} +
find "$share" -type f -exec chmod 0644 {} +
install -m 0644 "$REPO/systemd/homestead-server.service" "$root/usr/lib/systemd/system/homestead-server.service"
install -m 0644 "$REPO/systemd/homestead-apply.service" "$root/usr/lib/systemd/system/homestead-apply.service"
install -m 0644 "$REPO/systemd/homestead-tunnel.service" "$root/usr/lib/systemd/system/homestead-tunnel.service"
install -m 0644 "$REPO/packaging/debian/README.Debian" "$root/usr/share/doc/homestead/README.Debian"
install -m 0644 "$REPO/CHANGELOG.md" "$root/usr/share/doc/homestead/CHANGELOG.md"
install -m 0755 "$REPO/packaging/debian/postinst" "$root/DEBIAN/postinst"
install -m 0755 "$REPO/packaging/debian/prerm" "$root/DEBIAN/prerm"
install -m 0755 "$REPO/packaging/debian/postrm" "$root/DEBIAN/postrm"

PACKAGE_VERSION="$VERSION-$REVISION" PACKAGE_ARCH="$ARCH" PACKAGE_MAINTAINER="$MAINTAINER" \
	python3 - "$REPO/packaging/debian/control.in" > "$root/DEBIAN/control" <<'PY'
import os
import re
import sys

maintainer = os.environ["PACKAGE_MAINTAINER"]
if not re.fullmatch(r"[^<>\n]+\s+<[^<>\s]+@[^<>\s]+>", maintainer):
    raise SystemExit("error: DEB_MAINTAINER must be 'Name <email@example.org>'")
template = open(sys.argv[1], encoding="utf-8").read()
for marker, value in {
    "@VERSION@": os.environ["PACKAGE_VERSION"].rsplit("-", 1)[0],
    "@REVISION@": os.environ["PACKAGE_VERSION"].rsplit("-", 1)[1],
    "@ARCH@": os.environ["PACKAGE_ARCH"],
    "@MAINTAINER@": maintainer,
}.items():
    template = template.replace(marker, value)
if re.search(r"@[A-Z_]+@", template):
    raise SystemExit("error: unresolved package metadata placeholder")
sys.stdout.write(template)
PY

mkdir -p "$OUTPUT_DIR"
package="$OUTPUT_DIR/homestead_${VERSION}-${REVISION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$root" "$package"
printf 'built %s\n' "$package"
