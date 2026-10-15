#!/usr/bin/env bash
# Sign a craft release manifest with minisign (S279, ADR-028), producing
# MANIFEST.minisig beside it. The signature must be the non-prehashed
# ("Ed") form: the daemon rejects prehashed ("ED") signatures.
#
#   sign-craft-manifest.sh MANIFEST --key SECRET_KEY_FILE
#
# The private key stays on the signer's machine and never enters this
# repository: a key path inside the repository is refused. HAI signs with
# the HAI craft signing key; an owner who builds from their own artifact
# signs with their own key and adds its public half with
# `homesteadctl craft trust add --key-file KEY.pub`. The procedure is
# identical for both.
set -euo pipefail

manifest="${1:-}"
[ -n "$manifest" ] && [ -f "$manifest" ] || { echo "usage: $0 MANIFEST --key SECRET_KEY_FILE" >&2; exit 2; }
shift
key=""
while [ $# -gt 0 ]; do
	case "$1" in
	--key) key="${2:-}"; shift 2 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
[ -n "$key" ] && [ -f "$key" ] || { echo "--key must name the minisign secret key file" >&2; exit 2; }
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
real_key="$(readlink -f "$key")"
case "$real_key" in
"$repo"/*)
	echo "refusing to use a key inside the repository: $real_key" >&2
	exit 1
	;;
esac

command -v minisign >/dev/null || { echo "minisign is required (https://jedisct1.github.io/minisign/)" >&2; exit 1; }

sig="$manifest.minisig"
# -l is minisign's legacy (non-prehashed) format, the "Ed" algorithm.
minisign -S -l -s "$key" -m "$manifest" -x "$sig"

# Guard against a minisign build whose default or flag differs: the second
# line of the signature file is base64 whose first two bytes name the
# algorithm.
algo="$(sed -n 2p "$sig" | base64 -d 2>/dev/null | head -c 2)"
if [ "$algo" != "Ed" ]; then
	rm -f "$sig"
	echo "the signature is not the non-prehashed Ed form (got '$algo'); not written" >&2
	exit 1
fi
echo "signed: $sig"
