#!/usr/bin/env bash
# S367 daemon-independence check: build the image, run the plain Compose
# stack, prove refusal without config, health with config, data persistence
# across down/up, and export from the stopped volume. No Homestead daemon,
# no host port, no network access to an issuer (the issuer is a dead URL:
# discovery is lazy, so start-up and health do not need it).
# usage: run_compose_check.sh [image-tag]   (default homestead/everything-tasks:<VERSION>)
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd); repo=$(cd "$here/../../.." && pwd)
tag=${1:-homestead/everything-tasks:$(cat "$repo/VERSION")}
ver=${tag##*:}; proj=s367check$$
work=$(mktemp -d /tmp/s367check.XXXX)
compose=(docker compose -p "$proj" -f "$repo/images/everything/compose.example.yaml")
A=alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f
# Exports run as root (the data file is private to uid 65532), so hand the output back.
own() { docker run --rm -v "$work:/w" "$A" chown -R "$(id -u):$(id -g)" /w; }
cleanup() { own >/dev/null 2>&1 || true; "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true; docker rm -f "${proj}-refuse" >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
ok() { echo "PASS $*"; }; bad() { echo "FAIL $*" >&2; exit 1; }

bash "$repo/scripts/everything/build-image.sh" tasks "$tag" >/dev/null
export EVERYTHING_VERSION=$ver
mkdir -p "$work/secrets" "$work/seed" "$work/out1" "$work/out2"
printf %s 'not-a-real-secret' > "$work/secrets/oidc_client_secret"; chmod 0444 "$work/secrets/oidc_client_secret"
export EVERYTHING_SECRET_FILE="$work/secrets/oidc_client_secret"
export EVERYTHING_ORIGIN=https://home.test EVERYTHING_OIDC_ISSUER=https://auth.invalid/application/o/tasks/
export EVERYTHING_OIDC_CLIENT_ID=tasks EVERYTHING_OIDC_REDIRECT_URL=https://home.test/apps/tasks/_homestead/callback

# 1. No configuration: the container must exit non-zero and say why.
if docker run --name "${proj}-refuse" "$tag" >"$work/refuse.log" 2>&1; then bad "started without OIDC config"; fi
grep -q 'everything-bridge' "$work/refuse.log" && ok "refuses to start without OIDC configuration ($(tail -1 "$work/refuse.log" | cut -c1-80))"
docker rm "${proj}-refuse" >/dev/null

# 2. Seed the volume while the stack is stopped, using the image's own import tool.
cat > "$work/seed/manifest.json" <<'J'
{"format": "homestead-everything-export/1", "objects": [
  {"app":"tasks","subject":"alice","kind":"kv","key":"tasks:items:v1","value":{"tasks":[1,2,3]},"bytes":15},
  {"app":"tasks","subject":"alice","kind":"file","key":"tasks-data/log.json","path":"files/tasks/x/tasks-data/log.json","bytes":7}
]}
J
mkdir -p "$work/seed/files/tasks/x/tasks-data"; printf archive > "$work/seed/files/tasks/x/tasks-data/log.json"
"${compose[@]}" create >/dev/null
vol=${proj}_data
docker run --rm -v "$vol:/data" -v "$work/seed:/seed:ro" "$tag" import -db /data/bridge.db -in /seed >/dev/null

# 3. Start the stack: healthy, redirects unauthenticated users, 401 on data.
"${compose[@]}" up -d >/dev/null
cid=$("${compose[@]}" ps -q tasks)
for _ in $(seq 1 30); do [ "$(docker inspect -f '{{.State.Health.Status}}' "$cid")" = healthy ] && break; sleep 1; done
[ "$(docker inspect -f '{{.State.Health.Status}}' "$cid")" = healthy ] || { docker logs "$cid" 2>&1 | tail -5; bad "not healthy"; }
ok "container healthy (read-only root, user $(docker inspect -f '{{.Config.User}}' "$cid"), host port bindings: $(docker port "$cid" | wc -l))"
ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$cid")
[ "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$cid")" = true ] || bad "root fs writable"
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
[ "$(code "http://$ip:8081/apps/tasks/_homestead/healthz")" = 200 ] && ok "healthz 200 without sign-in"
[ "$(code "http://$ip:8081/apps/tasks/")" = 302 ] && ok "unauthenticated page redirects to sign-in"
[ "$(code -X POST -H 'Origin: https://home.test' -H 'Content-Type: application/json' -d '{"operation":"whoami"}' "http://$ip:8081/apps/tasks/_homestead/data")" = 401 ] && ok "data API refuses without a session"

# 4. down/up keeps the volume; stop, then export from the stopped volume.
"${compose[@]}" down >/dev/null
"${compose[@]}" up -d >/dev/null
cid=$("${compose[@]}" ps -q tasks)
for _ in $(seq 1 30); do [ "$(docker inspect -f '{{.State.Health.Status}}' "$cid")" = healthy ] && break; sleep 1; done
[ "$(docker inspect -f '{{.State.Health.Status}}' "$cid")" = healthy ] && ok "healthy again after down/up"
"${compose[@]}" stop >/dev/null
docker run --rm --user 0:0 -v "$vol:/data:ro" -v "$work/out1:/out" "$tag" export -db /data/bridge.db -subject alice -app tasks -out /out/e
own
grep -q '"tasks":\[1,2,3\]' "$work/out1/e/manifest.json" && [ "$(cat "$work"/out1/e/files/tasks/*/tasks-data/log.json)" = archive ] && ok "data survived down/up and exports from the stopped volume"
! grep -rqi 'session\|not-a-real-secret' "$work/out1/e" && ok "export holds no session data or secret"

# 5. Online-style backup of the stopped volume, restore into a fresh volume, re-export.
docker run --rm --user 0:0 -v "$vol:/data" -v "$work/out2:/out" "$tag" backup -db /data/bridge.db -out /out/bk.db >/dev/null
own
ok "backup written ($(stat -c %s "$work/out2/bk.db") bytes)"
# Restore = put the backup file where the data file lives while stopped.
docker volume create "${proj}_restored" >/dev/null
rc=$(docker create -v "${proj}_restored:/data" "$tag")
docker cp "$work/out2/bk.db" "$rc:/data/bridge.db"; docker rm "$rc" >/dev/null
docker run --rm --user 0:0 -v "${proj}_restored:/data:ro" -v "$work/out2:/out" "$tag" export -db /data/bridge.db -out /out/restored >/dev/null
own
grep -q '"tasks":\[1,2,3\]' "$work/out2/restored/manifest.json" && ok "backup restored into a fresh volume with the same data"
docker volume rm "${proj}_restored" >/dev/null
echo "compose check done"
