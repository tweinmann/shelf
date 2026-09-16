#!/usr/bin/env bash
# Runs the secret-expansion smoke test against the dev cluster and cleans up afterwards.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

ns=shelf-smoke-secrets
pod=secret-expansion
trap 'kubectl delete namespace "$ns" --ignore-not-found --wait=false >/dev/null' EXIT

kubectl apply -f "$(dirname "$0")/secret-expansion.yaml" >/dev/null

phase=""
for _ in $(seq 60); do
  phase="$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.status.phase}')"
  [[ "$phase" == Succeeded || "$phase" == Failed ]] && break
  sleep 2
done
kubectl -n "$ns" logs "pod/$pod" || true
[[ "$phase" == Succeeded ]] || die "pod ended in phase '${phase:-unknown}'"

if kubectl -n "$ns" get pod "$pod" -o yaml | grep -q 'smoke-value-7f3a'; then
  die "the secret value appears in the pod spec"
fi
echo "PASS: secret expansion in env and args, \$\$ escaping, no secret value in the pod spec"
