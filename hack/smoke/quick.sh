#!/usr/bin/env bash
# Checks the Phase 8d acceptance: an app added with --quick is exposed through a quick tunnel,
# without a Cloudflare connection. shelf reports its trycloudflare.com address, the address reaches the app with
# the app's own host name in the Host header, a restarted cloudflared gets a new address that
# `shelf app status` shows, and --private takes the app off the internet again.
#
# Usage: hack/smoke/quick.sh [app]   (default hello-quick; deploys examples/hello under that name)
# Installs or updates the platform first (idempotent), keeping the cluster's domain and host
# suffix: a quick tunnel does not depend on either, so this runs on any dev cluster. Needs network
# access for images and for trycloudflare.com; no Cloudflare account or token.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
app="${1:-hello-quick}"
artifact="oci://$SHELF_REGISTRY_HOST/smoke/hello-deploy"
export SHELF_HOME="$work/home"
setting() { kubectl -n flux-system get configmap shelf-config -o jsonpath="{.data.$1}" 2>/dev/null || true; }
domain="$(setting SHELF_DOMAIN)"
domain="${domain:-$SHELF_DEV_DOMAIN}"
suffix="$(setting SHELF_HOST_SUFFIX)"
host="$app$suffix.$domain"

shelf() { "$work/shelf" "$@"; }

cleanup() {
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

for leftover in "namespace/$app" "-n shelf-system resourcesetinputprovider/$app"; do
  # shellcheck disable=SC2086
  kubectl get $leftover >/dev/null 2>&1 \
    && die "$leftover already exists; remove it first (shelf app rm $app)"
done
trap cleanup EXIT

step "build shelf, push platform and chart, init cluster"
(cd "$repo" && go build -o "$work/shelf" ./cmd/shelf)
"$repo/hack/platform-push.sh" >/dev/null 2>&1
"$repo/hack/chart-push.sh" >/dev/null 2>&1
shelf init cluster --yes --domain "$domain" --host-suffix "$suffix" --insecure-registry \
  --platform "oci://$SHELF_REGISTRY_HOST/shelf/platform:dev" \
  --chart "oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev" >/dev/null

step "publish examples/hello"
mkdir -p "$work/v1"
(cd "$repo" && go run ./cmd/shelf render examples/hello/app.yaml) >"$work/v1/configmap.yaml" 2>/dev/null
flux push artifact "$artifact:main" --path "$work/v1" --source local \
  --revision "main@sha1:$(head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
  --insecure-registry >/dev/null 2>&1 || die "push failed"

step "shelf app add --quick gives the app a quick tunnel"
start=$SECONDS
shelf app add "$app" "$artifact:main" --insecure-registry --quick | tee "$work/add.txt"
echo "add: $((SECONDS - start)) s"
grep -q '^internet: a quick tunnel' "$work/add.txt" || die "the app did not get a quick tunnel"
url="$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com/' "$work/add.txt" | tail -1)"
[[ -n "$url" ]] || die "shelf did not report the address of the quick tunnel"
echo "address: $url"
kubectl -n shelf-system get secret "tunnel-$app" >/dev/null 2>&1 && die "a quick tunnel needs no credentials"

# reaches <url>: the app answers there, and sees its own host name rather than trycloudflare's.
# Resolve through Cloudflare's DoH endpoint: the container's resolver may have cached the name
# as missing before the tunnel registered it.
reaches() {
  curl -fsS --max-time 10 --doh-url https://cloudflare-dns.com/dns-query "$1" >"$work/answer.txt" &&
    grep -q '^Hostname: web-' "$work/answer.txt" &&
    grep -qi "^Host: $host" "$work/answer.txt"
}

step "the address reaches the app with its own host name"
start=$SECONDS
retry 120 reaches "$url" || { cat "$work/answer.txt" 2>/dev/null; die "$url did not reach $app within 2 minutes"; }
echo "reachable after $((SECONDS - start)) s"
shelf app status "$app" | tee "$work/status1.txt"
grep -qF "address   $url" "$work/status1.txt" || die "shelf app status does not show $url"

step "a restarted cloudflared gets a new address, and status follows it"
kubectl -n "$app" rollout restart deploy/cloudflared >/dev/null
kubectl -n "$app" rollout status deploy/cloudflared --timeout=120s >/dev/null
moved() {
  shelf app status "$app" >"$work/status2.txt" &&
    new="$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com/' "$work/status2.txt")" &&
    [[ -n "$new" && "$new" != "$url" ]]
}
retry 60 moved || die "shelf app status still shows the old address"
new="$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com/' "$work/status2.txt")"
echo "new address: $new"
retry 120 reaches "$new" || die "$new did not reach $app within 2 minutes"

step "--private takes the app off the internet"
shelf app credentials "$app" --private | tee "$work/private.txt"
kubectl -n "$app" get deploy cloudflared >/dev/null 2>&1 && die "cloudflared still runs"
shelf app status "$app" | grep -q trycloudflare && die "status still shows a quick tunnel"

step "--quick puts it back"
shelf app credentials "$app" --quick | tee "$work/quick.txt"
back="$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com/' "$work/quick.txt" | tail -1)"
[[ -n "$back" ]] || die "shelf did not report the address of the quick tunnel"
retry 120 reaches "$back" || die "$back did not reach $app within 2 minutes"

step "shelf app rm"
shelf app rm "$app" --yes >/dev/null
kubectl get namespace "$app" >/dev/null 2>&1 && die "namespace $app is still there"

echo "PASS: quick tunnel on request, address reported, Host rewritten to $host," \
  "new address after a restart, --private and --quick, app rm"
