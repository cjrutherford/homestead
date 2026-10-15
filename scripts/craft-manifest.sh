#!/usr/bin/env bash
# Generate a craft release manifest (S279, ADR-028) from a directory of
# upstream release artifacts and the shared build context. Prints the
# manifest to stdout; sign it with scripts/sign-craft-manifest.sh. Runs
# on the signer's machine, never on the appliance.
#
#   craft-manifest.sh --counter N --artifact-dir DIR \
#       --app NAME VERSION FILE [--url URL ...] \
#       [--app NAME VERSION FILE [--url URL ...]] [--context DIR]
#
# FILE is a .tar.gz, .tgz or .zip inside --artifact-dir; each --url is an
# https mirror of the same bytes (none means the owner supplies the file
# locally). --context defaults to images/craft in this repository.
#
# The build-context digest is SHA-256 over the text produced by
#   (cd CONTEXT && find . -type f | LC_ALL=C sort | sha256sum per file)
# in sha256sum layout (`<hex>  <path>` with no ./ prefix), the same value
# the daemon computes (internal/craft TreeDigest). Symlinks and special
# files are refused, exactly as the daemon refuses them.
set -euo pipefail

usage() {
	sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
counter=""
artifact_dir=""
context="$repo/images/craft"
apps=()  # entries: "name|version|file|url1 url2"

while [ $# -gt 0 ]; do
	case "$1" in
	--counter) counter="${2:-}"; shift 2 ;;
	--artifact-dir) artifact_dir="${2:-}"; shift 2 ;;
	--context) context="${2:-}"; shift 2 ;;
	--app)
		[ $# -ge 4 ] || usage
		apps+=("$2|$3|$4|")
		shift 4
		;;
	--url)
		[ "${#apps[@]}" -gt 0 ] || { echo "--url must follow an --app" >&2; exit 2; }
		last=$(( ${#apps[@]} - 1 ))
		apps[$last]="${apps[$last]}${2:-} "
		shift 2
		;;
	-h | --help) usage ;;
	*) echo "unknown argument: $1" >&2; usage ;;
	esac
done

[[ "$counter" =~ ^[1-9][0-9]*$ ]] || { echo "--counter must be a positive integer" >&2; exit 2; }
[ -d "$artifact_dir" ] || { echo "--artifact-dir must be a directory" >&2; exit 2; }
[ -d "$context" ] || { echo "context directory not found: $context" >&2; exit 2; }
[ "${#apps[@]}" -gt 0 ] || { echo "at least one --app is required" >&2; exit 2; }

context_digest() {
	local dir="$1"
	if [ -n "$(find "$dir" ! -type f ! -type d -print -quit)" ]; then
		echo "context contains a symlink or special file: refused" >&2
		return 1
	fi
	if [ -e "$dir/artifact" ]; then
		echo "context must not contain 'artifact' (reserved for the verified artifact)" >&2
		return 1
	fi
	if [ -n "$(find "$dir" -type f -name '*
*' -print -quit)" ]; then
		echo "context contains a file name with a newline: refused" >&2
		return 1
	fi
	local sum
	sum="$(cd "$dir" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 -r sha256sum | sed 's#^\([0-9a-f]\{64\}\)  \./#\1  #' | sha256sum | cut -d' ' -f1)"
	echo "sha256:$sum"
}

digest="$(context_digest "$context")"

printf 'format: homestead-craft-manifest/1\n'
printf 'counter: %s\n' "$counter"
printf 'context_digest: %s\n' "$digest"
printf 'apps:\n'
for entry in "${apps[@]}"; do
	IFS='|' read -r name version file urls <<<"$entry"
	[[ "$name" =~ ^[a-z][a-z0-9-]*$ ]] || { echo "invalid app name: $name" >&2; exit 2; }
	[[ "$version" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || { echo "invalid version for $name: $version (use 1.2 or 1.2.3)" >&2; exit 2; }
	[[ "$file" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*\.(tar\.gz|tgz|zip)$ ]] || { echo "artifact must be a bare .tar.gz, .tgz or .zip name: $file" >&2; exit 2; }
	path="$artifact_dir/$file"
	[ -f "$path" ] && [ ! -L "$path" ] || { echo "artifact not found (or a symlink): $path" >&2; exit 2; }
	sha="$(sha256sum "$path" | cut -d' ' -f1)"
	size="$(stat -c %s "$path")"
	printf '  - name: %s\n    version: %s\n    artifact:\n      name: %s\n' "$name" "$version" "$file"
	if [ -n "${urls// /}" ]; then
		printf '      urls:\n'
		for u in $urls; do
			[[ "$u" =~ ^https://[^[:space:]@]+$ ]] || { echo "url must be https without credentials: $u" >&2; exit 2; }
			printf '        - %s\n' "$u"
		done
	fi
	printf '      sha256: %s\n      size: %s\n' "$sha" "$size"
done
