#!/usr/bin/env bash
# Delete the k3d dev cluster and its kubeconfig.
set -euo pipefail
source "$(dirname "$0")/lib.sh"
require_devcontainer

if ! k3d cluster get "$SHELF_CLUSTER" >/dev/null 2>&1; then
  echo "cluster $SHELF_CLUSTER does not exist"
  exit 0
fi

k3d cluster delete "$SHELF_CLUSTER"
rm -f "$KUBECONFIG"
