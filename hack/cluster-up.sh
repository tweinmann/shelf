#!/usr/bin/env bash
# Create (or start) the k3d dev cluster in this container's Docker daemon and write the
# kubeconfig. Safe to run repeatedly, also after the devcontainer was restarted.
set -euo pipefail
source "$(dirname "$0")/lib.sh"
require_devcontainer
: "${SHELF_K3S_IMAGE:?SHELF_K3S_IMAGE is not set; it comes from .devcontainer/Dockerfile}"

if k3d cluster get "$SHELF_CLUSTER" >/dev/null 2>&1; then
  echo "cluster $SHELF_CLUSTER exists, making sure it is running"
  k3d cluster start "$SHELF_CLUSTER" --wait
  docker inspect "$SHELF_REGISTRY" >/dev/null 2>&1 \
    || die "cluster $SHELF_CLUSTER has no registry $SHELF_REGISTRY; run just cluster-reset"
else
  # servicelb stays enabled: disabling it makes k3d hang (k3d-io/k3d#742, #1241).
  # Nothing in shelf may use a LoadBalancer Service; the mini has none.
  # The API server and the registry are published on this container's loopback, where kubectl
  # and flux run. The registry is deleted together with the cluster.
  k3d cluster create "$SHELF_CLUSTER" \
    --image "$SHELF_K3S_IMAGE" \
    --k3s-arg "--disable=traefik@server:*" \
    --no-lb \
    --api-port 127.0.0.1:6445 \
    --registry-create "$SHELF_REGISTRY:127.0.0.1:$SHELF_REGISTRY_PORT" \
    --kubeconfig-update-default=false \
    --kubeconfig-switch-context=false \
    --wait
fi

# The registry is published on this container's loopback; give it the name pods use. /etc/hosts
# is recreated with the container, so this runs on every cluster-up.
if ! grep -qE "^127\.0\.0\.1[[:space:]]+$SHELF_REGISTRY\$" /etc/hosts; then
  echo "127.0.0.1 $SHELF_REGISTRY" | sudo tee -a /etc/hosts >/dev/null
fi

mkdir -p "$(dirname "$KUBECONFIG")"
k3d kubeconfig get "$SHELF_CLUSTER" > "$KUBECONFIG"
chmod 600 "$KUBECONFIG"
echo "API server: $(kubectl config view -o jsonpath='{.clusters[0].cluster.server}')"

kubectl wait --for=condition=Ready node --all --timeout=90s
kubectl get nodes -o wide
