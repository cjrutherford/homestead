#!/usr/bin/env bash
# Verify the response headers of a running static craft app (S249,
# ADR-028) and print them for the record. Reused by every craft package
# (docs/architecture/CRAFT_HEADER_TABLE.md); runs against a container,
# the edge route, or any URL, and needs only bash and curl.
#
#   craft-header-check.sh URL [--cacert FILE] [--resolve HOST:PORT:IP]
#       [--coop-coep] [--unhashed]
#
# URL is the app root (for example https://wordcraft.lan/). --cacert is the
# Homestead internal CA root (ADR-030); --resolve is passed to curl for a
# name that is not in DNS yet. By default the app must NOT send
# Cross-Origin-Opener-Policy / Cross-Origin-Embedder-Policy; pass
# --coop-coep for an app that uses SharedArrayBuffer and needs both. Pass
# --unhashed for an app whose .wasm and .js keep the same names between
# versions (LightCraft): they must then be no-cache and NOT immutable.
#
# Checks (exit 1 on any failure, every line printed either way):
#   /healthz answers 200
#   index.html is no-cache
#   every .wasm referenced by index.html is application/wasm, immutable
#   and gzip-encoded when the client accepts gzip (no-cache with --unhashed)
#   every .js referenced by index.html is immutable and a JavaScript type
#   (no-cache with --unhashed)
#   X-Content-Type-Options: nosniff on the entry
#   COOP/COEP absent (or present with --coop-coep)
set -uo pipefail

usage() {
	sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}

url=""
curl_args=()
want_coi=0
unhashed=0
while [ $# -gt 0 ]; do
	case "$1" in
	--cacert) [ $# -ge 2 ] || usage; curl_args+=(--cacert "$2"); shift 2 ;;
	--resolve) [ $# -ge 2 ] || usage; curl_args+=(--resolve "$2"); shift 2 ;;
	--coop-coep) want_coi=1; shift ;;
	--unhashed) unhashed=1; shift ;;
	-h | --help) usage ;;
	-*) echo "unknown argument: $1" >&2; usage ;;
	*) [ -z "$url" ] || usage; url="$1"; shift ;;
	esac
done
[[ "$url" =~ ^https?://[^[:space:]]+$ ]] || { echo "URL must be http(s)://host[:port]/" >&2; usage; }
url="${url%/}"

fail=0
ok() { printf 'ok    %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fail=1; }

# head PATH [extra curl args]: status line and headers, CRs stripped.
head_of() {
	local path="$1"
	shift
	curl -sS -I --max-time 30 "${curl_args[@]}" "$@" "$url$path" | tr -d '\r'
}
header() { # header NAME <<< headers: value of the first match, lowercased name
	grep -i "^$1:" | head -1 | cut -d: -f2- | sed 's/^ *//'
}

echo "== GET /healthz"
h="$(head_of /healthz)" || { echo "cannot reach $url" >&2; exit 1; }
printf '%s\n' "$h"
case "$(printf '%s\n' "$h" | head -1)" in
*" 200"*) ok "/healthz 200" ;;
*) bad "/healthz did not answer 200" ;;
esac

echo "== GET /"
entry="$(head_of /)"
printf '%s\n' "$entry"
[ "$(printf '%s\n' "$entry" | header cache-control)" = "no-cache" ] && ok "entry Cache-Control: no-cache" || bad "entry is not no-cache"
[ "$(printf '%s\n' "$entry" | header x-content-type-options)" = "nosniff" ] && ok "X-Content-Type-Options: nosniff" || bad "X-Content-Type-Options: nosniff missing"
coop="$(printf '%s\n' "$entry" | header cross-origin-opener-policy)"
coep="$(printf '%s\n' "$entry" | header cross-origin-embedder-policy)"
if [ "$want_coi" = 1 ]; then
	[ -n "$coop" ] && [ -n "$coep" ] && ok "COOP '$coop' and COEP '$coep' present" || bad "COOP/COEP required but missing"
else
	[ -z "$coop" ] && [ -z "$coep" ] && ok "no COOP/COEP (not needed)" || bad "unexpected COOP '$coop' / COEP '$coep'"
fi

# Assets referenced by the entry document: relative or root-relative
# ./name, name or /name ending in .wasm or .js (quotes delimit them).
page="$(curl -sS --max-time 30 "${curl_args[@]}" "$url/")" || { echo "cannot fetch $url/" >&2; exit 1; }
assets="$(printf '%s' "$page" | grep -oE "['\"(](\./|/)?[A-Za-z0-9._/-]+\.(wasm|js)['\"]" | tr -d "'\"(" | sed -e 's#^\./##' -e 's#^/##' | sort -u)"
wasm="$(printf '%s\n' "$assets" | grep '\.wasm$' || true)"
js="$(printf '%s\n' "$assets" | grep '\.js$' || true)"
[ -n "$wasm" ] || bad "index.html references no .wasm asset"

for a in $wasm; do
	echo "== GET /$a (Accept-Encoding: gzip)"
	w="$(head_of "/$a" -H 'Accept-Encoding: gzip')"
	printf '%s\n' "$w"
	[ "$(printf '%s\n' "$w" | header content-type)" = "application/wasm" ] && ok "$a Content-Type: application/wasm" || bad "$a is not application/wasm"
	if [ "$unhashed" = 1 ]; then
		[ "$(printf '%s\n' "$w" | header cache-control)" = "no-cache" ] && ok "$a no-cache (unhashed name)" || bad "$a is not no-cache (an unhashed wasm must revalidate)"
	else
		printf '%s\n' "$w" | header cache-control | grep -q 'max-age=31536000' && printf '%s\n' "$w" | header cache-control | grep -q immutable && ok "$a immutable" || bad "$a is not immutable (hashed wasm must be)"
	fi
	[ "$(printf '%s\n' "$w" | header content-encoding)" = "gzip" ] && ok "$a gzip" || bad "$a not gzip-encoded"
done
for a in $js; do
	echo "== GET /$a"
	j="$(head_of "/$a")"
	printf '%s\n' "$j"
	case "$(printf '%s\n' "$j" | header content-type)" in
	text/javascript* | application/javascript*) ok "$a JavaScript type" ;;
	*) bad "$a has a non-JavaScript Content-Type" ;;
	esac
	if [ "$unhashed" = 1 ]; then
		[ "$(printf '%s\n' "$j" | header cache-control)" = "no-cache" ] && ok "$a no-cache (unhashed name)" || bad "$a is not no-cache (an unhashed js must revalidate)"
	else
		printf '%s\n' "$j" | header cache-control | grep -q immutable && ok "$a immutable" || bad "$a is not immutable (hashed js must be)"
	fi
done

if [ "$fail" = 0 ]; then echo "PASS"; else echo "FAILED"; fi
exit "$fail"
