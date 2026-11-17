#!/usr/bin/env bash
# Generate the Homestead release signing keypair offline (S493, ADR-025).
#
#   keygen.sh --out SECRET_KEY_PATH
#
# minisign writes the private key to SECRET_KEY_PATH (mode 600) and asks
# for the passphrase itself on the terminal; this script never sees it.
# The path must be outside this repository, must not exist yet, and its
# directory must exist. The public key is written to SECRET_KEY_PATH.pub
# and printed together with its key ID. Nothing secret is printed.
# Run it on an offline machine you control; the private key never enters
# the repository or the appliance.
#
# For tests only: --insecure-no-passphrase makes an unprotected key.
set -euo pipefail

out=""
nopass=""
while [ $# -gt 0 ]; do
	case "$1" in
	--out) out="${2:-}"; shift 2 ;;
	--insecure-no-passphrase) nopass="-W"; shift ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
[ -n "$out" ] || { echo "usage: $0 --out SECRET_KEY_PATH" >&2; exit 2; }

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
dir="$(dirname "$out")"
[ -d "$dir" ] || { echo "directory does not exist: $dir" >&2; exit 2; }
real="$(cd "$dir" && pwd -P)/$(basename "$out")"
case "$real" in
"$repo"/*)
	echo "refusing to write a key inside the repository: $real" >&2
	exit 1
	;;
esac
[ ! -e "$real" ] && [ ! -e "$real.pub" ] || { echo "refusing to overwrite an existing key: $real" >&2; exit 1; }
command -v minisign >/dev/null || { echo "minisign is required (https://jedisct1.github.io/minisign/)" >&2; exit 1; }

umask 077
# shellcheck disable=SC2086
minisign -G $nopass -s "$real" -p "$real.pub" -c "HAI Homestead release public key"
chmod 600 "$real"
chmod 644 "$real.pub"

pub="$(tail -n 1 "$real.pub")"
keyid="$(head -n 1 "$real.pub" | sed -n 's/.*public key \([0-9A-Fa-f]\{16\}\)$/\1/p')"
if [ -z "$keyid" ]; then
	# The comment was customised: derive the ID from the key bytes
	# (2-byte algorithm, 8-byte ID little-endian, 32-byte key).
	keyid="$(printf '%s' "$pub" | base64 -d | head -c 10 | tail -c 8 | od -An -tx1 | tr -d ' \n' | fold -w2 | tac | tr -d '\n' | tr a-f A-F)"
fi
echo "key ID:     $keyid"
echo "public key: $pub"
echo "public key file: $real.pub"
echo "Hand over the public key (not the private key). Private key stays at $real."
