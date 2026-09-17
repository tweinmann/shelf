#!/usr/bin/env bash
# Checks the Phase 4 flow against the dev cluster, with the dev registry standing in for GHCR:
# shelf app add deploys examples/hello from a deploy artifact, a new artifact under the same tag
# is rolled out by Flux alone, a tampered artifact is refused, shelf app rm removes everything,
# and a second add restores the secret from the host backup.
#
# Installs or updates the platform first (idempotent). Needs network access for images.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
app=hello
artifact="oci://$SHELF_REGISTRY_HOST/smoke/$app-deploy"
pf_pid=""
export SHELF_HOME="$work/home"

shelf() { "$work/shelf" "$@"; }

cleanup() {
  [[ -n "$pf_pid" ]] && kill "$pf_pid" 2>/dev/null || true
  if [[ "${KEEP:-}" == 1 ]]; then
    echo "KEEP=1: leaving app $app in place; its secret backup is in $SHELF_HOME"
    return
  fi
  shelf app rm "$app" --yes >/dev/null 2>&1 || true
  rm -rf "$work"
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

# publish <dir>: pushes the files in dir as the deploy artifact, tag main, and prints its digest.
publish() {
  flux push artifact "$artifact:main" --path "$1" --source local \
    --revision "main@sha1:$(head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
    --insecure-registry 2>&1 | sed -n 's/.*pushed to .*@\(sha256:[0-9a-f]*\).*/\1/p'
}

# render <app.yaml> <dir>: writes the deploy manifest of an app.yaml into dir.
render() {
  mkdir -p "$2"
  (cd "$repo" && go run ./cmd/shelf render "$1") >"$2/configmap.yaml" 2>/dev/null
}

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
shelf init cluster --yes --domain dev.local --insecure-registry \
  --platform "oci://$SHELF_REGISTRY_HOST/shelf/platform:dev" \
  --chart "oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev" >/dev/null

step "publish examples/hello"
render "$repo/examples/hello/app.yaml" "$work/v1"
digest="$(publish "$work/v1")"
[[ -n "$digest" ]] || die "push failed"

step "shelf app add"
start=$SECONDS
shelf app add "$app" "$artifact:main" --insecure-registry | tee "$work/add1.txt"
echo "add: $((SECONDS - start)) s"
grep -q '^secret db-password: generated$' "$work/add1.txt" || die "the secret was not generated"
password="$(sed -n 's/^ *db-password: //p' "$SHELF_HOME/apps/$app/secrets.yaml")"
[[ ${#password} -ge 26 ]] || die "no password in the backup"
grep -qF "$password" "$work/add1.txt" && die "the secret value appears in the output"
[[ "$(stat -c %a "$SHELF_HOME/apps/$app/secrets.yaml")" == 600 ]] || die "the backup is not 0600"

step "the app runs with the generated secret and is routed by Traefik"
kubectl -n "$app" rollout status deploy/web --timeout=60s >/dev/null
url="$(kubectl -n "$app" exec deploy/check -- printenv DATABASE_URL)"
[[ "$url" == "postgres://app:$password@db:5432/hello" ]] || die "unexpected DATABASE_URL"
kubectl -n traefik port-forward svc/traefik 18082:80 >/dev/null 2>&1 &
pf_pid=$!
routed() { curl -sS -H "Host: $app.dev.local" http://127.0.0.1:18082/ | grep -q '^Hostname: web-'; }
retry 30 routed || die "$app.dev.local does not reach web"

step "a new artifact under the same tag is rolled out without any command"
sed 's/^    instances: 2$/    instances: 3/' "$repo/examples/hello/app.yaml" >"$work/v2.app.yaml"
grep -q 'instances: 3' "$work/v2.app.yaml" || die "could not change the example"
render "$work/v2.app.yaml" "$work/v2"
publish "$work/v2" >/dev/null
start=$SECONDS
three_replicas() { [[ "$(kubectl -n "$app" get deploy web -o jsonpath='{.status.readyReplicas}')" == 3 ]]; }
retry 240 three_replicas || die "web was not scaled to 3 within 4 minutes"
echo "rolled out after $((SECONDS - start)) s"

step "shelf app add again changes nothing and keeps the secret"
shelf app add "$app" "$artifact:main" --insecure-registry | tee "$work/add2.txt"
grep -q '^secret db-password: kept$' "$work/add2.txt" || die "the secret was not kept"
grep -q "^ResourceSetInputProvider shelf-system/$app: unchanged$" "$work/add2.txt" \
  || die "the provider changed"
[[ "$(sed -n 's/^ *db-password: //p' "$SHELF_HOME/apps/$app/secrets.yaml")" == "$password" ]] \
  || die "the backup changed"

step "a deploy artifact with other objects is refused"
mkdir -p "$work/tampered"
cp "$work/v2/configmap.yaml" "$work/tampered/"
cat >"$work/tampered/extra.yaml" <<'EOF'
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: shelf-smoke-tampered
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}
subjects: [{kind: ServiceAccount, name: default, namespace: hello}]
EOF
publish "$work/tampered" >/dev/null
kubectl -n "$app" annotate ocirepository deploy --overwrite \
  "reconcile.fluxcd.io/requestedAt=$(date +%s)" >/dev/null
refused() {
  kubectl -n "$app" get kustomization deploy \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}' | grep -q 'forbidden'
}
retry 60 refused || die "the tampered artifact was not refused"
kubectl get clusterrolebinding shelf-smoke-tampered >/dev/null 2>&1 \
  && die "the tampered artifact created a ClusterRoleBinding"
publish "$work/v2" >/dev/null

step "shelf app rm"
kill "$pf_pid"
pf_pid=""
start=$SECONDS
shelf app rm "$app" --yes | tee "$work/rm.txt"
echo "rm: $((SECONDS - start)) s"
kubectl get namespace "$app" >/dev/null 2>&1 && die "namespace $app is still there"
kubectl -n shelf-system get secret "app-$app" >/dev/null 2>&1 && die "secret app-$app is still there"
kubectl -n shelf-system get resourcesetinputprovider "$app" >/dev/null 2>&1 && die "the provider is still there"
[[ -z "$(kubectl get pv -o name)" ]] || die "a persistent volume is left"
[[ -f "$SHELF_HOME/apps/$app/secrets.yaml" ]] || die "the backup was deleted"
shelf app rm "$app" --yes >/dev/null 2>&1 && die "removing a missing app must fail"

step "shelf app add restores the secret from the backup"
shelf app add "$app" "$artifact:main" --insecure-registry | tee "$work/add3.txt"
grep -q '^secret db-password: restored from the backup$' "$work/add3.txt" || die "the secret was not restored"
url="$(kubectl -n "$app" exec deploy/check -- printenv DATABASE_URL)"
[[ "$url" == "postgres://app:$password@db:5432/hello" ]] || die "the restored password differs"

echo "PASS: app add, routing, rollout by polling, idempotent add, tampered artifact refused," \
  "app rm, restore from backup"
