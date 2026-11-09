#!/usr/bin/env bash
# Disposable S390 appliance bootstrap. No host Docker socket enters the appliance.
set -Eeuo pipefail
umask 077
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/homestead-s390-init.XXXXXXXX")
id=$(basename "$tmp" | tr '[:upper:].' '[:lower:]-')
network="$id-net"
container="$id-appliance"
created_network=0
created_container=0
cleanup() {
  if (( created_container )); then timeout 35s docker rm -f "$container" >/dev/null 2>&1 || printf 'Cleanup failed: container %s\n' "$container" >&2; fi
  if (( created_network )); then timeout 15s docker network rm "$network" >/dev/null 2>&1 || printf 'Cleanup failed: network %s\n' "$network" >&2; fi
  printf 'Private diagnostic directory retained: %s (mode 0700; remove after inspection)\n' "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
printf 'Building isolated daemon (deadline 180s).\n'
if ! (cd "$root" && timeout 180s env CGO_ENABLED=0 go build -o "$tmp/homestead-server" ./cmd/homestead-server) >"$tmp/build.log" 2>&1; then
  printf 'BLOCKED: daemon build failed; inspect %s/build.log\n' "$tmp" >&2; exit 1
fi
printf 'Starting private nested Docker appliance %s.\n' "$container"
timeout 20s docker network create "$network" > /dev/null
created_network=1
timeout 45s docker run -d --name "$container" --network "$network" --privileged -e DOCKER_TLS_CERTDIR= -p 127.0.0.1::18080 docker:27.5.1-dind > /dev/null
created_container=1
ready=0
for ((i=0; i<45; i++)); do
  if timeout 3s docker exec "$container" docker info >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
if (( ! ready )); then
  timeout 5s docker logs --tail 80 "$container" >"$tmp/nested-docker.log" 2>&1 || true
  printf 'BLOCKED: private Docker engine did not become ready; inspect %s/nested-docker.log\n' "$tmp" >&2; exit 1
fi
timeout 15s docker exec "$container" mkdir -p /var/lib/homestead /etc/homestead /run/homestead /fixture/catalog /fixture/storage
# A private mounted filesystem satisfies the same storage checks as an appliance.
timeout 15s docker exec "$container" mount -t tmpfs -o size=4G tmpfs /fixture/storage
# Copy the actual build and curated catalog, never bind the checkout or host socket.
timeout 20s docker cp "$tmp/homestead-server" "$container:/fixture/homestead-server"
timeout 20s docker cp "$root/catalog/." "$container:/fixture/catalog/"
timeout 15s docker exec -d "$container" sh -c 'exec /fixture/homestead-server -listen 0.0.0.0:18080 -config /etc/homestead/desired.yaml -data-dir /var/lib/homestead -socket /run/homestead/api.sock -catalog-dir /fixture/catalog -claim-token-file /run/homestead/claim-token > /fixture/daemon.log 2>&1'
address=$(timeout 10s docker port "$container" 18080/tcp)
if ! timeout 260s python3 "$root/tests/e2e/scripts/s390-init-api.py" "$container" "$address" "$tmp"; then
  timeout 10s docker exec "$container" cat /fixture/daemon.log >"$tmp/daemon.log" 2>&1 || true
  printf 'BLOCKED: initialization or reviewed install prerequisite failed; inspect private artifacts in %s\n' "$tmp" >&2
  exit 1
fi
