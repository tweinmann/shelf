#!/usr/bin/env bash
# Packages charts/shelf-app as version 0.0.0-dev and pushes it to the dev registry.
set -euo pipefail
source "$(dirname "$0")/lib.sh"
require_devcontainer

repo="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

helm package "$repo/charts/shelf-app" --version 0.0.0-dev --destination "$work" >/dev/null
helm push "$work/shelf-app-0.0.0-dev.tgz" "oci://$SHELF_REGISTRY_HOST/shelf/charts" --plain-http
# The dev tag never changes, so the chart sources of the running apps have to be told to look
# again; in a release the chart tag changes and Flux notices by itself.
for ns in $(kubectl get ocirepository -A -o json |
  jq -r '.items[] | select(.metadata.name == "shelf-app") | .metadata.namespace'); do
  kubectl -n "$ns" annotate ocirepository shelf-app --overwrite \
    "reconcile.fluxcd.io/requestedAt=$(date +%s)" >/dev/null
  echo "app $ns: chart refresh requested"
done

echo "use: shelf init cluster --chart oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev"
