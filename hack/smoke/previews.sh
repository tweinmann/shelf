#!/usr/bin/env bash
# Checks the Phase 10 branch previews against the dev cluster, with the dev registry standing in
# for GHCR: examples/hello (web plus Postgres with a volume) gets previews; two branch tags run as
# two isolated previews, each with its own database, volume and password; `shelf app open
# --preview` reaches one with its own Host; a new commit on a branch keeps the preview's data;
# the limit holds; deleting a tag removes its preview; --no-previews removes them all and leaves
# the app running. It prints the seconds from push to Ready and the RAM of a preview.
#
# Nothing here nudges Flux: the times are what an agent waits. Installs or updates the platform
# first (idempotent). Needs network access for images.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
app=prev-smoke
artifact="oci://$SHELF_REGISTRY_HOST/smoke/$app-deploy"
open_pid=""
export SHELF_HOME="$work/home"

shelf() { "$work/shelf" "$@"; }

cleanup() {
  [[ -n "$open_pid" ]] && kill "$open_pid" 2>/dev/null || true
  if [[ "${KEEP:-}" == 1 ]]; then
    echo "KEEP=1: leaving app $app in place; its secret backup is in $SHELF_HOME"
    return
  fi
  shelf app rm "$app" --yes >/dev/null 2>&1 || true
  rm -rf "$work"
}

step() { echo "--- $*"; }

commit() { head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# publish <tag> <commit>: pushes examples/hello as the deploy artifact under tag, built from
# commit.
publish() {
  flux push artifact "$artifact:$1" --path "$work/hello" --source local --revision "$1@sha1:$2" \
    --insecure-registry >/dev/null 2>&1 || die "push of $1 failed"
}

# unpublish <tag>: deletes a tag from the dev registry, as build.yml does on GHCR for a deleted
# branch.
unpublish() {
  local path="${artifact#oci://$SHELF_REGISTRY_HOST/}" digest
  digest="$(curl -fsS -I -H "Accept: application/vnd.oci.image.manifest.v1+json" \
    "http://$SHELF_REGISTRY_HOST/v2/$path/manifests/$1" | tr -d '\r' |
    awk -F': ' 'tolower($1)=="docker-content-digest"{print $2}')"
  [[ -n "$digest" ]] || die "no digest for $1"
  curl -fsS -X DELETE "http://$SHELF_REGISTRY_HOST/v2/$path/manifests/$digest" >/dev/null \
    || die "the registry refused to delete $1; run just cluster-up"
}

# password <namespace>: the hash of the generated db-password there, never the value.
password() {
  kubectl -n "$1" get secret shelf-secrets -o jsonpath='{.data.db-password}' | sha256sum | cut -c1-16
}

# wait_preview <branch> <commit>: waits for the preview of branch at commit and prints seconds.
wait_preview() {
  local start=$SECONDS code=0
  shelf app status "$app" --preview "$1" --wait --commit "$2" --timeout 5m --json >"$work/wait.json" || code=$?
  [[ "$code" == 0 ]] || die "preview of $1 at $2: exit $code, $(cat "$work/wait.json")"
  echo $((SECONDS - start))
}

# gone <namespace>: waits up to 3 minutes for a namespace to disappear.
gone() {
  for _ in $(seq 90); do
    kubectl get namespace "$1" >/dev/null 2>&1 || return 0
    sleep 2
  done
  return 1
}

for leftover in "namespace/$app" "-n shelf-system resourcesetinputprovider/$app"; do
  # shellcheck disable=SC2086
  kubectl get $leftover >/dev/null 2>&1 && die "$leftover already exists; remove it first (shelf app rm $app)"
done
trap cleanup EXIT

step "build shelf, push platform and chart, init cluster"
(cd "$repo" && go build -o "$work/shelf" ./cmd/shelf)
"$repo/hack/platform-push.sh" >/dev/null 2>&1
"$repo/hack/chart-push.sh" >/dev/null 2>&1
shelf init cluster --yes --host-suffix "$(cluster_host_suffix)" --insecure-registry \
  --platform "oci://$SHELF_REGISTRY_HOST/shelf/platform:dev" \
  --chart "oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev" >/dev/null
kubectl -n shelf-system get resourceset previews >/dev/null || die "the platform has no ResourceSet previews"

step "add the app with previews of at most two branches"
mkdir -p "$work/hello"
(cd "$repo" && go run ./cmd/shelf render examples/hello/app.yaml) >"$work/hello/configmap.yaml" 2>/dev/null
publish main "$(commit)"
shelf app add "$app" "$artifact:main" --insecure-registry --previews=2 >/dev/null
[[ "$(kubectl -n shelf-system get resourcesetinputprovider "$app--previews" -o jsonpath='{.spec.type}')" == OCIArtifactTag ]] \
  || die "no preview provider"
shelf app status --json | jq -e --arg app "$app" '[.items[].name] | index($app + "--previews") == null' >/dev/null \
  || die "the preview provider is listed as an app"

step "two branches run as two previews"
ca="$(commit)"
publish branch-alpha "$ca"
first="$(wait_preview alpha "$ca")"
echo "first push of a branch to Ready: $first s"
cb="$(commit)"
publish branch-beta "$cb"
wait_preview beta "$cb" >/dev/null
for ns in "$app--alpha" "$app--beta"; do
  [[ "$(kubectl get namespace "$ns" -o jsonpath='{.metadata.labels.shelf\.dev/app}')" == "$app" ]] \
    || die "$ns is not labelled as a preview of $app"
  kubectl -n "$ns" get pvc data-db-0 >/dev/null || die "$ns has no volume of its own"
  kubectl -n "$ns" get deploy cloudflared >/dev/null 2>&1 && die "$ns runs cloudflared"
  kubectl -n "$ns" get resourcequota shelf-preview >/dev/null || die "$ns has no quota"
done
hashes="$(printf '%s\n' "$(password "$app")" "$(password "$app--alpha")" "$(password "$app--beta")" | sort -u | wc -l)"
[[ "$hashes" == 3 ]] || die "the app and its previews share a password"

step "shelf app open --preview reaches the preview with its own Host"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
shelf app open "$app" --preview alpha --port "$port" >"$work/open.txt" 2>&1 &
open_pid=$!
up() { curl -fsS "http://127.0.0.1:$port/" >/dev/null 2>&1; }
for _ in $(seq 30); do up && break; sleep 1; done
curl -fsS "http://127.0.0.1:$port/" | grep -q "^Host: $app--alpha.shelf.internal" \
  || die "the preview did not get its own Host: $(cat "$work/open.txt")"
kill "$open_pid"; wait "$open_pid" 2>/dev/null || true; open_pid=""

step "a new commit on a branch keeps the preview's data"
before="$(password "$app--alpha")" volume="$(kubectl -n "$app--alpha" get pvc data-db-0 -o jsonpath='{.metadata.uid}')"
ca2="$(commit)"
publish branch-alpha "$ca2"
later="$(wait_preview alpha "$ca2")"
echo "later commit on a branch to Ready: $later s"
[[ "$(password "$app--alpha")" == "$before" ]] || die "the new commit changed the preview's password"
[[ "$(kubectl -n "$app--alpha" get pvc data-db-0 -o jsonpath='{.metadata.uid}')" == "$volume" ]] \
  || die "the new commit replaced the preview's volume"

step "RAM of a preview"
sleep 60 # metrics-server reports a new pod after up to a minute
kubectl top pods -n "$app--alpha" --no-headers | awk '{sub("Mi","",$3); sum+=$3} END {print "preview RAM: " sum " Mi"}'
kubectl top pods -n "$app" --no-headers | awk '{sub("Mi","",$3); sum+=$3} END {print "app RAM:     " sum " Mi"}'

step "the limit holds"
publish branch-gamma "$(commit)"
for _ in $(seq 60); do
  [[ "$(kubectl get namespace -l "shelf.dev/app=$app,shelf.dev/preview" --no-headers 2>/dev/null | wc -l)" == 2 ]] \
    && kubectl get namespace "$app--gamma" >/dev/null 2>&1 && break
  sleep 2
done
shelf app status "$app" --json | jq -e '.previewLimit == 2 and .previewLimitReached == true and (.previews | length) <= 2' >/dev/null \
  || die "the limit does not hold: $(shelf app status "$app" --json | jq -c '{previews: [.previews[]?.slug], previewLimit, previewLimitReached}')"
echo "previews within the limit: $(shelf app status "$app" --json | jq -r '[.previews[].slug] | join(", ")')"

step "deleting a branch tag removes its preview"
unpublish branch-gamma
start=$SECONDS
gone "$app--gamma" || die "the preview of a deleted tag is still there"
echo "deleted tag to preview gone: $((SECONDS - start)) s"

step "--no-previews removes every preview and keeps the app"
shelf app credentials "$app" --no-previews >/dev/null
for ns in "$app--alpha" "$app--beta"; do gone "$ns" || die "$ns is still there after --no-previews"; done
shelf app status "$app" --json | jq -e '.phase == "Ready" and (.previews == null)' >/dev/null || die "the app did not survive --no-previews"

step "remove the app"
shelf app rm "$app" --yes >/dev/null
echo "smoke-previews passed"
