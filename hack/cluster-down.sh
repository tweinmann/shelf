#!/usr/bin/env bash
# Delete the k3d dev cluster. This devcontainer is detached from the cluster network first:
# Docker does not remove a network that still has endpoints, so k3d would leave it behind.
set -euo pipefail
source "$(dirname "$0")/lib.sh"
require_devcontainer

if ! k3d cluster get "$SHELF_CLUSTER" >/dev/null 2>&1; then
  echo "cluster $SHELF_CLUSTER does not exist"
  exit 0
fi

docker network disconnect "$SHELF_NETWORK" "$SHELF_DEVCONTAINER" 2>/dev/null || true
k3d cluster delete "$SHELF_CLUSTER"
rm -f "$KUBECONFIG"
