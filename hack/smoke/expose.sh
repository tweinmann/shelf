#!/usr/bin/env bash
# Checks the Phase 8b acceptance for Cloudflare: an app that already runs is given the Cloudflare
# connection "smoke", gets a tunnel of its own in that connection's account, becomes reachable
# over HTTPS under a domain of its own, and is taken off the internet again without leaving its
# record or its tunnel behind.
#
# Usage: hack/smoke/expose.sh <app> <domain>   (the app must already be deployed, e.g. greeter)
# The domain has to be a Cloudflare zone of the account. CF_API_TOKEN is read from the
# environment or prompted for; it needs Account:Cloudflare Tunnel:Edit and Zone:DNS:Edit for the
# zone. With several accounts, set CF_ACCOUNT_ID.
#
# The cluster must have a host suffix (just init-cluster --host-suffix -dev), so that the dev
# cluster never takes the name the app has on the mini.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

app="${1:-}"
domain="${2:-}"
[[ -n "$app" && -n "$domain" ]] || die "usage: $0 <app> <domain>"

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

# cf <path> [curl options...]: calls the Cloudflare API with the token.
cf() {
  local path="$1"
  shift
  curl -sS -H "Authorization: Bearer $CF_API_TOKEN" "$@" "https://api.cloudflare.com/client/v4$path"
}

suffix="$(kubectl -n flux-system get configmap shelf-config -o jsonpath='{.data.SHELF_HOST_SUFFIX}')"
[[ -n "$suffix" ]] || die "the cluster has no host suffix; run just init-cluster --host-suffix -dev first"
host="$app$suffix.$domain"
tunnel_name="shelf$suffix-$app"
kubectl get namespace "$app" >/dev/null 2>&1 || die "app $app is not deployed in this cluster"

account="${CF_ACCOUNT_ID:-}"
if [[ -z "$account" ]]; then
  account="$(cf /accounts | jq -r 'if (.result | length) == 1 then .result[0].id else empty end')"
  [[ -n "$account" ]] || die "the token sees several accounts or none; set CF_ACCOUNT_ID"
fi
zone="$(cf "/zones?name=$domain" | jq -r '.result[0].id')"
[[ -n "$zone" && "$zone" != null ]] || die "zone $domain not found; does the token cover it?"

# find_tunnel <name>: the id of the tunnel with exactly this name, or nothing.
find_tunnel() {
  cf "/accounts/$account/cfd_tunnel?is_deleted=false&name=$1" \
    | jq -r --arg name "$1" '.result[] | select(.name == $name and .deleted_at == null) | .id'
}
record() { cf "/zones/$zone/dns_records?name=$host&type=CNAME" | jq -r '.result[0] // empty'; }

step "the tunnel all apps used to share is gone"
# Before Phase 8b, init expose made one tunnel per cluster, named shelf<suffix>. init cluster
# switched off its cloudflared; the tunnel itself is removed here, by its exact name. A cluster
# without a suffix is refused above, so this never touches the mini's tunnel.
kubectl -n shelf-system get deploy cloudflared >/dev/null 2>&1 \
  && die "cloudflared still runs in shelf-system; run just init-cluster first"
shared="$(find_tunnel "shelf$suffix")"
if [[ -n "$shared" ]]; then
  cf "/accounts/$account/cfd_tunnel/$shared/connections" -X DELETE | jq -e '.success' >/dev/null
  cf "/accounts/$account/cfd_tunnel/$shared" -X DELETE | jq -e '.success' >/dev/null \
    || die "could not delete the old tunnel shelf$suffix"
  echo "deleted the old tunnel shelf$suffix ($shared)"
fi

step "build shelf, define the connection smoke and expose $app through it"
(cd "$repo" && go build -o "$work/shelf" ./cmd/shelf)
export SHELF_HOME="${SHELF_HOME:-$HOME/.shelf}"
"$work/shelf" connection add cloudflare smoke | tee "$work/connection.txt"
connection="$SHELF_HOME/connections/cloudflare/smoke.yaml"
[[ "$(stat -c %a "$connection")" == 600 ]] || die "$connection is not 0600"
start=$SECONDS
"$work/shelf" app credentials "$app" --cloudflare smoke --domain "$domain" | tee "$work/expose.txt"
echo "expose: $((SECONDS - start)) s"
grep -qF "$CF_API_TOKEN" "$work/connection.txt" "$work/expose.txt" && die "the token appears in the output"
kubectl get secrets -A -o json | grep -qF "$CF_API_TOKEN" && die "the token is in the cluster"

step "the app has its own tunnel and its own cloudflared"
id="$(find_tunnel "$tunnel_name")"
[[ -n "$id" ]] || die "tunnel $tunnel_name does not exist in account $account"
target="$id.cfargotunnel.com"
kubectl -n "$app" rollout status deploy/cloudflared --timeout=60s >/dev/null \
  || die "cloudflared does not run in namespace $app"
kubectl -n "$app" get ingress -o jsonpath='{.items[*].spec.rules[*].host}' | grep -q "$host" \
  || die "no ingress serves $host"

step "the DNS record points at the app's tunnel"
rec="$(record)"
[[ "$(jq -r '.content' <<<"$rec")" == "$target" ]] \
  || die "record $host points at $(jq -r '.content' <<<"$rec"), not at $target"
[[ "$(jq -r '.proxied' <<<"$rec")" == true ]] \
  || die "record $host is not proxied; a tunnel target only works through Cloudflare"
echo "record: $host points at $target (proxied)"

step "the app answers over HTTPS"
# Resolve through Cloudflare's DoH endpoint: the container's resolver caches the answer from
# before the record existed, which would keep failing long after the name works.
answers() { curl -fsS --max-time 10 --doh-url https://cloudflare-dns.com/dns-query "https://$host/" >"$work/answer.txt"; }
start=$SECONDS
retry 180 answers || die "https://$host/ did not answer within 3 minutes"
echo "reachable after $((SECONDS - start)) s"
head -1 "$work/answer.txt"

step "shelf owns the record: a wrong target is corrected by the next deploy"
cf "/zones/$zone/dns_records/$(jq -r '.id' <<<"$rec")" -X PATCH -H 'Content-Type: application/json' \
  --data '{"content":"wrong.cfargotunnel.com"}' | jq -e '.success' >/dev/null \
  || die "could not change the record for the test"
"$work/shelf" app credentials "$app" --domain "$domain" | grep -E "^DNS $host points at .*: updated$" \
  || die "the record was not corrected"
[[ "$(record | jq -r '.content')" == "$target" ]] || die "the record still points somewhere else"

step "off the internet again: record, tunnel and cloudflared are gone"
"$work/shelf" app credentials "$app" --private | tee "$work/off.txt"
[[ -z "$(record)" ]] || die "the record of $host is still there"
[[ -z "$(find_tunnel "$tunnel_name")" ]] || die "tunnel $tunnel_name is still there"
kubectl -n "$app" get deploy cloudflared >/dev/null 2>&1 && die "cloudflared still runs"
"$work/shelf" connection rm cloudflare smoke
[[ -f "$connection" ]] && die "$connection is still there"

echo "PASS: own tunnel in the connection's account, record published and corrected, HTTPS as $host," \
  "and nothing left behind when taken off"
