#!/usr/bin/env bash
# Checks the Phase 5 acceptance: shelf init expose connects the dev cluster to Cloudflare, and an
# app that already runs becomes reachable from the internet over HTTPS under its own name.
#
# Usage: hack/smoke/expose.sh <app>        (the app must already be deployed, e.g. greeter)
# CF_API_TOKEN is read from the environment or prompted for. It needs Account:Cloudflare
# Tunnel:Edit and Zone:DNS:Edit for the zone; with several accounts, set CF_ACCOUNT_ID.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

app="${1:-}"
[[ -n "$app" ]] || die "usage: $0 <app>"

if [[ -z "${CF_API_TOKEN:-}" ]]; then
  read -rsp "Cloudflare API token: " CF_API_TOKEN
  echo
fi
export CF_API_TOKEN

repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
step() { echo "--- $*"; }

# retry <seconds> <command...>
retry() {
  local seconds="$1"
  shift
  for _ in $(seq "$seconds"); do
    "$@" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

# cf <path>: calls the Cloudflare API with the token.
cf() { curl -sS -H "Authorization: Bearer $CF_API_TOKEN" "https://api.cloudflare.com/client/v4$1"; }

settings="$(kubectl -n flux-system get configmap shelf-config -o json)"
domain="$(jq -r '.data.SHELF_DOMAIN' <<<"$settings")"
suffix="$(jq -r '.data.SHELF_HOST_SUFFIX // ""' <<<"$settings")"
[[ -n "$domain" && "$domain" != null ]] || die "the cluster has no domain; run just init-cluster first"
host="$app$suffix.$domain"
kubectl get namespace "$app" >/dev/null 2>&1 || die "app $app is not deployed in this cluster"

step "build shelf and expose the cluster"
(cd "$repo" && go build -o "$work/shelf" ./cmd/shelf)
start=$SECONDS
"$work/shelf" init expose --yes | tee "$work/expose.txt"
echo "expose: $((SECONDS - start)) s"
target="$(sed -n 's/.*target *\(.*\.cfargotunnel\.com\).*/\1/p' "$work/expose.txt")"
[[ -n "$target" ]] || die "could not read the tunnel target"

step "the ingress of $app carries the tunnel as its target"
annotated() {
  kubectl -n "$app" get ingress -o jsonpath='{.items[*].metadata.annotations.external-dns\.kubernetes\.io/target}' \
    | grep -q "$target"
}
retry 120 annotated || die "no ingress points at $target; the app has to be redeployed by Flux"
kubectl -n "$app" get ingress -o jsonpath='{.items[*].spec.rules[*].host}' | grep -q "$host" \
  || die "no ingress serves $host"

step "external-dns publishes $host"
zone="$(cf "/zones?name=$domain" | jq -r '.result[0].id')"
[[ -n "$zone" && "$zone" != null ]] || die "zone $domain not found; does the token cover it?"
record_target() { cf "/zones/$zone/dns_records?name=$host" | jq -r '.result[0].content // ""'; }
record_ok() { [[ "$(record_target)" == "$target" ]]; }
start=$SECONDS
retry 180 record_ok || die "no DNS record $host -> $target after 3 minutes"
echo "record after $((SECONDS - start)) s: $host -> $(record_target)"
owner="$(cf "/zones/$zone/dns_records?type=TXT" | jq -r --arg h "$host" '.result[] | select(.name | contains($h)) | .content' | head -1)"
[[ -n "$owner" ]] || die "external-dns left no ownership record for $host"

step "the app answers over HTTPS"
# Resolve through Cloudflare's DoH endpoint: the container's resolver caches the answer from
# before the record existed, which would keep failing long after the name works.
answers() { curl -fsS --max-time 10 --doh-url https://cloudflare-dns.com/dns-query "https://$host/" >"$work/answer.txt"; }
start=$SECONDS
retry 180 answers || die "https://$host/ did not answer within 3 minutes"
echo "reachable after $((SECONDS - start)) s"
head -1 "$work/answer.txt"
curl -sS -o /dev/null --doh-url https://cloudflare-dns.com/dns-query \
  -w 'TLS: %{ssl_verify_result} (0 = valid), HTTP %{http_code} via %{scheme}\n' "https://$host/"

echo "PASS: tunnel up, DNS record and TXT owner published, app reachable over HTTPS as $host"
