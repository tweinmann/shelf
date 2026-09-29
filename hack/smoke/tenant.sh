#!/usr/bin/env bash
# Acceptance for Phase 4 with a real tenant: adds the app from its GHCR deploy artifact, then
# waits while you push a change to the tenant repository, until the app answers differently.
#
# Usage: hack/smoke/tenant.sh <app> oci://ghcr.io/<owner>/<app>:main
# GHCR_USERNAME and GHCR_TOKEN (classic PAT, read:packages) are read from the environment, or
# prompted for. shelf app add --registry-login stores them as the app's own login, which is also
# what shelf reads the artifact with.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer
require_dev_domain

app="${1:-}"
artifact="${2:-}"
[[ -n "$app" && "$artifact" == oci://ghcr.io/* ]] \
  || die "usage: $0 <app> oci://ghcr.io/<owner>/<app>:main"

if [[ -z "${GHCR_USERNAME:-}" ]]; then read -rp "GHCR username: " GHCR_USERNAME; fi
if [[ -z "${GHCR_TOKEN:-}" ]]; then
  read -rsp "GHCR token (classic PAT, read:packages): " GHCR_TOKEN
  echo
fi
export GHCR_USERNAME GHCR_TOKEN

# Check the credentials before touching the cluster. ghcr.io/v2/ answers 401 whatever is sent,
# because it points at its token endpoint; that endpoint answers 200 for a usable login and 403
# for a token that cannot read the package (fine-grained, wrong scope, or no access).
repository="${artifact#oci://ghcr.io/}"
repository="${repository%%:*}"
code="$(curl -s -o /dev/null -w '%{http_code}' -u "$GHCR_USERNAME:$GHCR_TOKEN" \
  "https://ghcr.io/token?service=ghcr.io&scope=repository:$repository:pull")"
[[ "$code" == 200 ]] \
  || die "ghcr.io did not grant pull access to $repository (HTTP $code); the token must be a classic PAT with read:packages"

repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
pf_pid=""
shelf() { "$work/shelf" "$@"; }
cleanup() {
  [[ -n "$pf_pid" ]] && kill "$pf_pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT
step() { echo "--- $*"; }

step "build shelf, push platform and chart, init cluster"
(cd "$repo" && go build -o "$work/shelf" ./cmd/shelf)
"$repo/hack/platform-push.sh" >/dev/null 2>&1
"$repo/hack/chart-push.sh" >/dev/null 2>&1
shelf init cluster --yes --domain dev.local --insecure-registry \
  --platform "oci://$SHELF_REGISTRY_HOST/shelf/platform:dev" \
  --chart "oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev" | grep -E 'Platform'
# No Docker config at all: shelf has to read the artifact with the app's own login.
export DOCKER_CONFIG="$work/docker"
mkdir -p "$DOCKER_CONFIG"

step "shelf app add --registry-login"
shelf app add "$app" "$artifact" --registry-login | tee "$work/add.txt"
grep -q "^registry login: $GHCR_USERNAME for ghcr.io$" "$work/add.txt" || die "the login was not stored"
grep -qF "$GHCR_TOKEN" "$work/add.txt" && die "the token appears in the output"
kubectl -n shelf-system get secret "registry-$app" >/dev/null || die "the app has no login of its own"

kubectl -n traefik port-forward svc/traefik 18083:80 >/dev/null 2>&1 &
pf_pid=$!
sleep 2
# Only a real answer counts: during a rollout Traefik briefly replies 504, and that must not be
# mistaken for the new version.
answer() {
  local body code
  body="$(curl -sS -m 5 -w '\n%{http_code}' -H "Host: $app.dev.local" http://127.0.0.1:18083/ 2>/dev/null)" || return 1
  code="${body##*$'\n'}"
  [[ "$code" == 200 ]] || return 1
  head -1 <<<"$body"
}
settled() {
  local first second
  first="$(answer)" || return 1
  sleep 3
  second="$(answer)" || return 1
  [[ "$first" == "$second" ]] || return 1
  printf '%s' "$first"
}

before="$(settled)" || die "the app does not answer"
echo "the app answers: $before"

step "now push a change to the tenant repository; waiting up to 15 minutes"
start=$SECONDS
while (( SECONDS - start < 900 )); do
  if now="$(settled)" && [[ "$now" != "$before" ]]; then
    echo "after $((SECONDS - start)) s the app answers: $now"
    echo "PASS: the pushed change reached the app without any command"
    echo "Remove the app with: go run ./cmd/shelf app rm $app"
    exit 0
  fi
  sleep 5
done
die "the answer did not change within 15 minutes"
