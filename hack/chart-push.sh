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
echo "use: shelf init cluster --chart oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev"
