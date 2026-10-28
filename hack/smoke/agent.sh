#!/usr/bin/env bash
# Checks the Phase 9 agent interface against the dev cluster, with the dev registry standing in
# for GHCR: `shelf app status --json`, `--wait --commit` that returns Ready only after the
# awaited commit rolled out, Failed (exit 1) for an image that cannot be pulled, TimedOut
# (exit 2) for a commit that never arrives, and `shelf app open` with the app's Host and
# stripPrefix through Traefik. With SMOKE_FULL=1 (the default) it also checks that a fixed commit
# after the failed one is Ready again, which waits for Helm to time out the broken upgrade
# (about 5 minutes). CI runs that part on main only; pull requests set SMOKE_FULL=0, leave the
# broken app in place and discard the cluster.
#
# The script asks Flux to fetch each new artifact at once instead of waiting for its poll: what
# it checks is how `--wait` follows a rollout, not Flux's interval. `--wait` itself only reads.
#
# Installs or updates the platform first (idempotent). Needs network access for images.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
app=agent-smoke
artifact="oci://$SHELF_REGISTRY_HOST/smoke/$app-deploy"
open_pid=""
full="${SMOKE_FULL:-1}"
export SHELF_HOME="$work/home"

shelf() { "$work/shelf" "$@"; }

cleanup() {
  [[ -n "$open_pid" ]] && kill "$open_pid" 2>/dev/null || true
  if [[ "${KEEP:-}" == 1 || "$full" != 1 ]]; then
    echo "leaving app $app in place (KEEP=1 or SMOKE_FULL=0); its secret backup is in $SHELF_HOME"
    return
  fi
  shelf app rm "$app" --yes >/dev/null 2>&1 || true
  rm -rf "$work"
}

step() { echo "--- $*"; }

# commit: a random 40-digit commit hash, as the reusable workflow stamps on an artifact.
commit() { head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# publish <dir> <commit>: pushes the files in dir as the deploy artifact, tag main, built from
# commit, and asks Flux to fetch it once the app exists.
publish() {
  push_artifact "$artifact:main" "$1" "main@sha1:$2" >/dev/null
  kubectl -n "$app" annotate ocirepository deploy --overwrite \
    "reconcile.fluxcd.io/requestedAt=$(date +%s%N)" >/dev/null 2>&1 || true
}

# render <app.yaml> <dir>: writes the deploy manifest of an app.yaml into dir.
render() {
  mkdir -p "$2"
  (cd "$repo" && go run ./cmd/shelf render "$1") >"$2/configmap.yaml" 2>/dev/null
}

# verdict <file> <field>: one field of a WaitResult.
verdict() { jq -r "$2" "$1"; }

for leftover in "namespace/$app" "-n shelf-system secret/app-$app" \
  "-n shelf-system resourcesetinputprovider/$app"; do
  # shellcheck disable=SC2086
  kubectl get $leftover >/dev/null 2>&1 \
    && die "$leftover already exists; remove it first (shelf app rm $app)"
done
trap cleanup EXIT

step "build shelf, push platform and chart, init cluster"
(cd "$repo" && go build -o "$work/shelf" ./cmd/shelf)
"$repo/hack/platform-push.sh" >/dev/null 2>&1
"$repo/hack/chart-push.sh" >/dev/null 2>&1
shelf init cluster --yes --signer-key "$SHELF_SIGNER_PUB" --host-suffix "$(cluster_host_suffix)" --insecure-registry \
  --platform "oci://$SHELF_REGISTRY_HOST/shelf/platform:dev" \
  --chart "oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev" >/dev/null

step "add the app at a first commit"
c1="$(commit)"
render "$repo/hack/smoke/agent.app.yaml" "$work/v1"
publish "$work/v1" "$c1"
shelf app add "$app" "$artifact:main" --insecure-registry >/dev/null

step "shelf app status --json"
shelf app status "$app" --json >"$work/status.json"
[[ "$(jq -r .kind "$work/status.json")" == AppStatus ]] || die "not an AppStatus: $(cat "$work/status.json")"
[[ "$(jq -r .commit "$work/status.json")" == "$c1" ]] || die "status does not name commit $c1"
[[ "$(jq '.stages | length' "$work/status.json")" == 5 ]] || die "status does not hold five stages"
shelf app status --json | jq -e --arg app "$app" '.kind == "AppList" and any(.items[]; .name == $app)' >/dev/null \
  || die "the AppList lacks $app"
shelf app status nope --json >"$work/error.json" 2>/dev/null && die "an unknown app was found"
[[ "$(jq -r .kind "$work/error.json")" == Error ]] || die "an error is not an Error document"

step "--wait --commit waits for the commit to roll out"
c2="$(commit)"
sed 's/web-v1/web-v2/' "$repo/hack/smoke/agent.app.yaml" >"$work/v2.app.yaml"
render "$work/v2.app.yaml" "$work/v2"
start=$SECONDS
shelf app status "$app" --wait --commit "$c2" --json >"$work/wait2.json" &
wait_pid=$!
sleep 5
kill -0 "$wait_pid" 2>/dev/null || die "the wait returned before commit $c2 was pushed: $(cat "$work/wait2.json")"
publish "$work/v2" "$c2"
code=0
wait "$wait_pid" || code=$?
echo "wait: $((SECONDS - start)) s"
[[ "$code" == 0 && "$(verdict "$work/wait2.json" .outcome)" == Ready ]] \
  || die "wait for $c2: exit $code, $(cat "$work/wait2.json")"
[[ "$(verdict "$work/wait2.json" .commit)" == "$c2" ]] || die "Ready for another commit: $(cat "$work/wait2.json")"
kubectl -n "$app" get deploy web -o jsonpath='{.spec.template.spec.containers[0].env}' | grep -q web-v2 \
  || die "Ready, but the release does not carry the new values"
kubectl -n "$app" rollout status deploy/web --timeout=10s >/dev/null || die "Ready before the rollout finished"

step "shelf app open reaches the app with its own Host and stripPrefix"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
shelf app open "$app" --port "$port" >"$work/open.txt" 2>&1 &
open_pid=$!
up() { curl -fsS "http://127.0.0.1:$port/" >/dev/null 2>&1; }
for _ in $(seq 30); do up && break; sleep 1; done
up || die "shelf app open does not answer: $(cat "$work/open.txt")"
api="$(curl -fsS "http://127.0.0.1:$port/api/x")"
grep -q '^GET /x ' <<<"$api" || die "/api/x did not reach api as /x: $api"
grep -q "^Host: $app.shelf.internal" <<<"$api" || die "Traefik did not get the app's Host: $api"
grep -q '^Hostname: api-' <<<"$api" || die "/api/x was not answered by api: $api"
web="$(curl -fsS "http://127.0.0.1:$port/")"
grep -q '^Name: web-v2' <<<"$web" || die "/ was not answered by web at v2: $web"
ss -ltn | grep -q "127.0.0.1:$port " || die "not listening on 127.0.0.1:$port"
ss -ltn | grep -E "(0\.0\.0\.0|\*|\[::\]):$port " && die "listening beyond the loopback interface"
kill "$open_pid"
wait "$open_pid" 2>/dev/null || true
open_pid=""

step "an image that cannot be pulled ends the wait with Failed"
c3="$(commit)"
render "$repo/hack/smoke/agent.app.yaml" "$work/v3"
sed -i "s/@sha256:[0-9a-f]*/@sha256:$(printf '0%.0s' $(seq 64))/" "$work/v3/configmap.yaml"
publish "$work/v3" "$c3"
code=0
shelf app status "$app" --wait --commit "$c3" --timeout 5m --json >"$work/wait3.json" || code=$?
[[ "$code" == 1 && "$(verdict "$work/wait3.json" .outcome)" == Failed ]] \
  || die "broken image: exit $code, $(cat "$work/wait3.json")"
[[ "$(verdict "$work/wait3.json" .stage.name)" == "the pods" ]] || die "not the pods: $(cat "$work/wait3.json")"
verdict "$work/wait3.json" .stage.hint | grep -q "cannot be pulled" || die "no pull hint: $(cat "$work/wait3.json")"

step "a commit that never arrives ends the wait with TimedOut"
code=0
shelf app status "$app" --wait --commit deadbeef --timeout 5s --json >"$work/wait4.json" || code=$?
[[ "$code" == 2 && "$(verdict "$work/wait4.json" .outcome)" == TimedOut ]] \
  || die "unknown commit: exit $code, $(cat "$work/wait4.json")"
verdict "$work/wait4.json" .stage.message | grep -q "waiting for commit deadbee" \
  || die "the timeout does not say what it waited for: $(cat "$work/wait4.json")"

if [[ "$full" != 1 ]]; then
  echo "SMOKE_FULL=0: skipping the fix after the failure; the broken app stays (its upgrade holds up removal)"
  echo "smoke-agent passed"
  exit 0
fi

# The agent's loop ends with a fix: a working commit after the broken one is Ready again. It
# also leaves no release stuck in an upgrade, which would hold up the removal.
step "a fixed commit after the failure is Ready again"
c4="$(commit)"
publish "$work/v2" "$c4"
code=0
shelf app status "$app" --wait --commit "$c4" --timeout 8m --json >"$work/wait5.json" || code=$?
[[ "$code" == 0 && "$(verdict "$work/wait5.json" .outcome)" == Ready ]] \
  || die "fixed commit: exit $code, $(cat "$work/wait5.json")"

step "remove the app"
shelf app rm "$app" --yes >/dev/null
echo "smoke-agent passed"
