#!/usr/bin/env bash
# Pushes platform/ as the platform artifact to the dev registry, tagged "dev".
set -euo pipefail
source "$(dirname "$0")/lib.sh"
require_devcontainer

repo="$(cd "$(dirname "$0")/.." && pwd)"
revision="$(git -C "$repo" rev-parse HEAD)"
if [[ -n "$(git -C "$repo" status --porcelain -- platform)" ]]; then
  revision="$revision-dirty"
fi

# Validate before pushing: Flux would only report a broken build in the cluster.
kubectl kustomize "$repo/platform" >/dev/null

flux push artifact "oci://$SHELF_REGISTRY_LOCAL/shelf/platform:dev" \
  --path "$repo/platform" \
  --source "local" \
  --revision "dev@sha1:$revision" \
  --insecure-registry
echo "in the cluster: --platform oci://$SHELF_REGISTRY_IN_CLUSTER/shelf/platform:dev --insecure-registry"
