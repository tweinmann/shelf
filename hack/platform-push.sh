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

# Signed with the dev key before the tag moves, as release.yml signs a release, so that the
# cluster never reads an unsigned platform.
platform="oci://$SHELF_REGISTRY_HOST/shelf/platform"
flux push artifact "$platform:staging" \
  --path "$repo/platform" \
  --source "local" \
  --revision "dev@sha1:$revision" \
  --insecure-registry
sign_artifact "${platform#oci://}:staging"
# flux tag artifact has no flag for a registry without TLS.
crane tag --insecure "${platform#oci://}:staging" dev
echo "use: shelf init cluster --platform $platform:dev --insecure-registry --signer-key $SHELF_SIGNER_PUB"
