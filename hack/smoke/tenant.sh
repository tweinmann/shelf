#!/usr/bin/env bash
# Acceptance for Phase 4 with a real tenant: adds the app from its GHCR deploy artifact, then
# waits while you push a change to the tenant repository, until the app answers differently.
#
# Usage: hack/smoke/tenant.sh <app> oci://ghcr.io/<owner>/<app>-deploy:main
# GHCR_USERNAME and GHCR_TOKEN (classic PAT, read:packages) are read from the environment, or
# prompted for. They are stored in the cluster by shelf init cluster, and used by docker login
# so that shelf app add can read the artifact.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

app="${1:-}"
artifact="${2:-}"
[[ -n "$app" && "$artifact" == oci://ghcr.io/* ]] \
  || die "usage: $0 <app> oci://ghcr.io/<owner>/<app>-deploy:main"

if [[ -z "${GHCR_USERNAME:-}" ]]; then read -rp "GHCR username: " GHCR_USERNAME; fi
if [[ -z "${GHCR_TOKEN:-}" ]]; then
  read -rsp "GHCR token (classic PAT, read:packages): " GHCR_TOKEN
  echo
fi
export GHCR_USERNAME GHCR_TOKEN

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

step "build shelf, push platform and chart, init cluster with the GHCR login"
(cd "$repo" && go build -o "$work/shelf" ./cmd/shelf)
"$repo/hack/platform-push.sh" >/dev/null 2>&1
"$repo/hack/chart-push.sh" >/dev/null 2>&1
shelf init cluster --yes --domain dev.local --insecure-registry \
  --platform "oci://$SHELF_REGISTRY_HOST/shelf/platform:dev" \
  --chart "oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev" | grep -E 'registry|Platform'
# The token goes through stdin only.
printf '%s' "$GHCR_TOKEN" | docker login ghcr.io --username "$GHCR_USERNAME" --password-stdin >/dev/null

step "shelf app add"
shelf app add "$app" "$artifact"

kubectl -n traefik port-forward svc/traefik 18083:80 >/dev/null 2>&1 &
pf_pid=$!
sleep 2
answer() { curl -sS -H "Host: $app.dev.local" http://127.0.0.1:18083/ | head -1; }
before="$(answer)"
echo "the app answers: $before"

step "now push a change to the tenant repository; waiting up to 15 minutes"
start=$SECONDS
while (( SECONDS - start < 900 )); do
  now="$(answer || true)"
  if [[ -n "$now" && "$now" != "$before" ]]; then
    echo "after $((SECONDS - start)) s the app answers: $now"
    echo "PASS: the pushed change reached the app without any command"
    echo "Remove the app with: shelf app rm $app   (or go run ./cmd/shelf app rm $app)"
    exit 0
  fi
  sleep 5
done
die "the answer did not change within 15 minutes"
