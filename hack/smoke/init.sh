#!/usr/bin/env bash
# Checks the Phase 3 acceptance criteria against the dev cluster: shelf init cluster installs
# Flux and the platform, a second run changes nothing, and apps are reachable through Traefik
# by Host header. Run `just cluster-reset` first to check a fresh cluster.
#
# Needs network access: Flux pulls its images and the Traefik chart, shelf render pins images.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
pf_pid=""
apps=(hello strip)

cleanup() {
  [[ -n "$pf_pid" ]] && kill "$pf_pid" 2>/dev/null || true
  rm -rf "$work"
  if [[ "${KEEP:-}" == 1 ]]; then
    echo "KEEP=1: leaving the apps in place"
    return
  fi
  for app in "${apps[@]}"; do
    helm uninstall "$app" -n "$app" --ignore-not-found >/dev/null 2>&1 || true
    kubectl delete namespace "$app" --ignore-not-found --timeout=120s >/dev/null
  done
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

for app in "${apps[@]}"; do
  kubectl get namespace "$app" >/dev/null 2>&1 \
    && die "namespace $app already exists; delete it first (kubectl delete namespace $app)"
done
trap cleanup EXIT

step "build shelf"
(cd "$repo" && go build -o "$work/shelf" ./cmd/shelf)
init_cluster() {
  "$work/shelf" init cluster --yes --insecure-registry \
    --platform "oci://$SHELF_REGISTRY_IN_CLUSTER/shelf/platform:dev"
}

if kubectl get namespace flux-system >/dev/null 2>&1; then
  echo "note: flux-system exists, so this is not a fresh cluster"
fi

step "push the platform artifact"
"$repo/hack/platform-push.sh" >/dev/null

step "shelf init cluster, first run"
start=$SECONDS
init_cluster | tee "$work/first.txt"
echo "first run: $((SECONDS - start)) s"

step "shelf init cluster, second run changes nothing"
start=$SECONDS
init_cluster | tee "$work/second.txt"
echo "second run: $((SECONDS - start)) s"
grep -Eq '(created|configured)' "$work/second.txt" && die "the second run changed objects"
grep -q 'objects, [0-9]* unchanged$' "$work/second.txt" || die "the second run did not report the operator objects"
grep -q '(FluxInstance flux-system/flux): unchanged$' "$work/second.txt" \
  || die "the second run did not report the FluxInstance as unchanged"

step "a new platform artifact under the same tag is applied at once"
digest="$("$repo/hack/platform-push.sh" 2>&1 | sed -n 's/.*pushed to .*@\(sha256:[0-9a-f]*\).*/\1/p')"
[[ -n "$digest" ]] || die "could not read the pushed digest"
init_cluster | tee "$work/third.txt"
grep -q "applied dev@$digest\$" "$work/third.txt" || die "shelf init cluster did not wait for $digest"

step "platform: Traefik is ready and ClusterIP, no LoadBalancer anywhere"
kubectl -n traefik get helmrelease traefik \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' | grep -qx True \
  || die "HelmRelease traefik is not ready"
[[ "$(kubectl -n traefik get svc traefik -o jsonpath='{.spec.type}')" == ClusterIP ]] \
  || die "service traefik is not ClusterIP"
kubectl get svc -A -o jsonpath='{range .items[*]}{.spec.type}{"\n"}{end}' | grep -qx LoadBalancer \
  && die "the cluster has a LoadBalancer service"
kubectl get ingressclass traefik >/dev/null || die "IngressClass traefik is missing"

# install_app <name> <app.yaml>: renders the file and installs it with a random secret.
install_app() {
  local app="$1" file="$2"
  (cd "$repo" && go run ./cmd/shelf render -o app "$file") >"$work/$app.values.yaml" 2>/dev/null
  kubectl create namespace "$app" >/dev/null
  if grep -q '^secrets:' "$file"; then
    kubectl -n "$app" create secret generic shelf-secrets \
      --from-literal=db-password="$(head -c 20 /dev/urandom | base32 | tr -d '=' | head -c 26)" >/dev/null
  fi
  helm install "$app" "$repo/charts/shelf-app" -n "$app" -f "$work/$app.values.yaml" \
    --set platform.domain=dev.local --wait --timeout 3m >/dev/null
}

step "install examples/hello and an app with stripPrefix"
install_app hello "$repo/examples/hello/app.yaml"
cat >"$work/strip.app.yaml" <<'EOF'
apiVersion: shelf.dev/v1alpha1
name: strip
components:
  api:
    image: traefik/whoami:v1.11.0
    port: 80
    route: { path: /api, stripPrefix: true }
EOF
install_app strip "$work/strip.app.yaml"

step "requests through Traefik"
kubectl -n traefik port-forward svc/traefik 18081:80 >/dev/null 2>&1 &
pf_pid=$!
get() { curl -sS -H "Host: $1" "http://127.0.0.1:18081$2"; }
status() { curl -sS -o /dev/null -w '%{http_code}' -H "Host: $1" "http://127.0.0.1:18081$2"; }
hello_routed() { get hello.dev.local / | grep -q '^Hostname: web-'; }
retry 30 hello_routed || die "hello.dev.local / does not reach web"
[[ "$(status other.dev.local /)" == 404 ]] || die "an unknown host must get 404"
strip_routed() { get strip.dev.local /api/items | grep -q '^GET /items HTTP/1.1'; }
retry 30 strip_routed || {
  get strip.dev.local /api/items >&2 || true
  die "strip.dev.local /api/items did not reach api as /items"
}
[[ "$(status strip.dev.local /other)" == 404 ]] || die "a path outside the route must get 404"

echo "PASS: init cluster twice without changes, Traefik ClusterIP, routing by host, stripPrefix"
