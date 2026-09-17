#!/usr/bin/env bash
# Installs examples/hello with the shelf-app chart into the dev cluster and checks the Phase 2
# acceptance criteria. Cleans up afterwards unless KEEP=1 is set.
#
# Needs network access: shelf render pins the example images through Docker Hub.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

ns=hello
release=hello
repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
pf_pid=""

cleanup() {
  [[ -n "$pf_pid" ]] && kill "$pf_pid" 2>/dev/null || true
  rm -rf "$work"
  if [[ "${KEEP:-}" == 1 ]]; then
    echo "KEEP=1: leaving namespace $ns in place"
    return
  fi
  helm uninstall "$release" -n "$ns" --ignore-not-found >/dev/null 2>&1 || true
  # Wait, so that an immediate rerun does not find the namespace still terminating.
  kubectl delete namespace "$ns" --ignore-not-found --timeout=120s >/dev/null
}

step() { echo "--- $*"; }

# Waits up to $1 seconds for a command to succeed.
retry() {
  local seconds="$1"
  shift
  for _ in $(seq "$seconds"); do
    "$@" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

if kubectl get namespace "$ns" >/dev/null 2>&1; then
  die "namespace $ns already exists; delete it first (kubectl delete namespace $ns)"
fi
trap cleanup EXIT

step "render examples/hello, with and without the db volume"
(cd "$repo" && go run ./cmd/shelf render -o app examples/hello/app.yaml) >"$work/values.yaml"
sed '/^    volumes:$/,+1d' "$repo/examples/hello/app.yaml" >"$work/novolume.app.yaml"
grep -q volumes: "$work/novolume.app.yaml" && die "could not remove the volume from the example"
(cd "$repo" && go run ./cmd/shelf render -o app "$work/novolume.app.yaml") >"$work/novolume.yaml" 2>/dev/null

# The same alphabet as shelf's generated secrets (A-Z, 2-7), so it is URL-safe.
password="$(head -c 20 /dev/urandom | base32 | tr -d '=' | head -c 26)"

step "create namespace and secret"
kubectl create namespace "$ns" >/dev/null
kubectl -n "$ns" create secret generic shelf-secrets --from-literal=db-password="$password" >/dev/null

step "helm install"
start=$SECONDS
helm_deploy() {
  helm upgrade --install "$release" "$repo/charts/shelf-app" -n "$ns" -f "$1" \
    --set platform.domain=dev.local --wait --timeout 3m >/dev/null
}
helm_deploy "$work/values.yaml"
echo "installed and ready in $((SECONDS - start)) s"
kubectl -n "$ns" get deploy,statefulset,svc,ingress,pvc

step "check reaches db through the generated DATABASE_URL"
select_results() { kubectl -n "$ns" logs deploy/check | grep -cx 1 || true; }
# Waits for check to log one more successful query than $1.
new_select_result() { (($(select_results) > $1)); }
retry 60 new_select_result 0 || {
  kubectl -n "$ns" logs deploy/check --tail=20 >&2 || true
  die "check never got a result from db"
}

step "printenv shows the expanded value"
url="$(kubectl -n "$ns" exec deploy/check -- printenv DATABASE_URL)"
[[ "$url" == "postgres://app:${password}@db:5432/hello" ]] \
  || die "unexpected DATABASE_URL in the container"

step "the secret value appears in no object spec"
specs="$(kubectl -n "$ns" get deploy,statefulset,pod,svc,ingress,configmap -o yaml)"
grep -qF "$password" <<<"$specs" && die "the secret value appears in an object spec"
kubectl -n "$ns" get deploy check -o yaml \
  | grep -qF 'value: postgres://app:$(SHELF_SECRET_DB_PASSWORD)@db:5432/hello' \
  || die "the Deployment does not carry the \$(SHELF_SECRET_DB_PASSWORD) reference"

step "services: db stays ClusterIP, db-headless is headless"
[[ "$(kubectl -n "$ns" get svc db -o jsonpath='{.spec.clusterIP}')" != None ]] \
  || die "service db must not be headless"
[[ "$(kubectl -n "$ns" get svc db-headless -o jsonpath='{.spec.clusterIP}')" == None ]] \
  || die "service db-headless must be headless"

step "ingress host"
host="$(kubectl -n "$ns" get ingress web -o jsonpath='{.spec.rules[0].host}')"
[[ "$host" == hello.dev.local ]] || die "ingress host is '$host', want hello.dev.local"

step "web answers through its service"
kubectl -n "$ns" port-forward svc/web 18080:80 >/dev/null 2>&1 &
pf_pid=$!
retry 20 curl -fsS http://127.0.0.1:18080/health || die "web did not answer on /health"
curl -fsS http://127.0.0.1:18080/ | grep -q '^Hostname: web-' || die "unexpected answer from web"
kill "$pf_pid"
pf_pid=""

step "postgres data survives kubectl delete pod"
marker="marker-$RANDOM$RANDOM"
psql_db() { kubectl -n "$ns" exec db-0 -- psql -U app -d hello -tAc "$1"; }
psql_db "create table smoke (v text); insert into smoke values ('$marker')" >/dev/null
old_uid="$(kubectl -n "$ns" get pod db-0 -o jsonpath='{.metadata.uid}')"
kubectl -n "$ns" delete pod db-0 >/dev/null
new_pod_ready() {
  local uid
  uid="$(kubectl -n "$ns" get pod db-0 -o jsonpath='{.metadata.uid}')" || return 1
  [[ -n "$uid" && "$uid" != "$old_uid" ]] || return 1
  kubectl -n "$ns" wait pod/db-0 --for=condition=Ready --timeout=1s
}
retry 120 new_pod_ready || die "db-0 did not come back"
# Postgres may still be starting even though the container is running.
retry 30 psql_db "select 1" || die "postgres in the new db-0 does not answer"
[[ "$(psql_db 'select v from smoke')" == "$marker" ]] || die "the marker row is gone"

# Adding or removing volumes switches between Deployment and StatefulSet. The upgrade must go
# through, and db must stay reachable under the same name and address.
switch_db() {
  local values="$1" kind="$2" ip before
  ip="$(kubectl -n "$ns" get svc db -o jsonpath='{.spec.clusterIP}')"
  before="$(select_results)"
  helm_deploy "$values"
  kubectl -n "$ns" get "$kind" db >/dev/null || die "db is not a $kind after the upgrade"
  [[ "$(kubectl -n "$ns" get svc db -o jsonpath='{.spec.clusterIP}')" == "$ip" ]] \
    || die "service db changed its address"
  retry 90 new_select_result "$before" || die "check did not reconnect to db"
}

step "remove the db volume: StatefulSet -> Deployment"
switch_db "$work/novolume.yaml" deployment
kubectl -n "$ns" get svc db-headless >/dev/null 2>&1 && die "db-headless is still there"
no_pvcs() { [[ -z "$(kubectl -n "$ns" get pvc -o name)" ]]; }
retry 30 no_pvcs || die "the PVC of the removed volume is still there"

step "add the db volume again: Deployment -> StatefulSet"
switch_db "$work/values.yaml" statefulset

echo "PASS: chart install, secret expansion, no secret in specs, services, ingress, web," \
  "PVC survival, volume add/remove"
