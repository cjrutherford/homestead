#!/usr/bin/env bash
# Startup probe only; this does not prove S390 sign-in, grants, or publication.
set -Eeuo pipefail
umask 077
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/homestead-s390.XXXXXXXX")
export S390_SECRET_DIR="$tmp"
project="homestead-s390-$(basename "$tmp" | tr '[:upper:].' '[:lower:]-')"
compose=(docker compose -p "$project" -f "$here/s390-disposable-compose.yaml")
cleanup() {
  timeout 45s "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || printf 'Cleanup failed for project %s; inspect manually.\n' "$project" >&2
  rm -rf -- "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
openssl rand -hex 32 > "$tmp/db_password"
openssl rand -hex 48 > "$tmp/secret_key"
chmod 444 "$tmp/db_password" "$tmp/secret_key"
printf 'Starting isolated project %s (deadline 240s).\n' "$project"
if ! timeout 240s "${compose[@]}" up -d --quiet-pull >/dev/null 2>&1; then
  printf 'BLOCKED: image pull or container startup failed or exceeded 240s; no credentials or logs emitted.\n' >&2
  exit 1
fi
probe() {
  local service=$1 port=$2 path=$3 mapped
  mapped=$(timeout 10s "${compose[@]}" port "$service" "$port")
  for ((attempt=1; attempt<=60; attempt++)); do
    if curl --max-time 2 --silent --output /dev/null --fail "http://${mapped}${path}"; then
      printf '%s HTTP readiness: passed\n' "$service"
      return 0
    fi
    sleep 2
  done
  printf '%s HTTP readiness: failed\n' "$service" >&2
  return 1
}
probe jellyfin 8096 /health
probe server 9000 /apps/auth/-/health/ready/
printf 'BLOCKED: startup is not S390 E2E. Homestead install, real sign-in, roles, denial, and publication ordering have not been exercised.\n' >&2
exit 2
