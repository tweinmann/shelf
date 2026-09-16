#!/usr/bin/env bash
# Create (or start) the k3d dev cluster, attach this devcontainer to the cluster network, and
# write a kubeconfig that reaches the API server over that network. Safe to run repeatedly.
set -euo pipefail
source "$(dirname "$0")/lib.sh"
require_devcontainer
: "${SHELF_K3S_IMAGE:?SHELF_K3S_IMAGE is not set; it comes from .devcontainer/Dockerfile}"

if k3d cluster get "$SHELF_CLUSTER" >/dev/null 2>&1; then
  echo "cluster $SHELF_CLUSTER exists, making sure it is running"
  k3d cluster start "$SHELF_CLUSTER" --wait
else
  # servicelb stays enabled: disabling it makes k3d hang (k3d-io/k3d#742, #1241).
  # Nothing in shelf may use a LoadBalancer Service; the mini has none.
  k3d cluster create "$SHELF_CLUSTER" \
    --image "$SHELF_K3S_IMAGE" \
    --k3s-arg "--disable=traefik@server:*" \
    --k3s-arg "--tls-san=${SHELF_SERVER}@server:*" \
    --no-lb \
    --api-port 127.0.0.1:6445 \
    --kubeconfig-update-default=false \
    --kubeconfig-switch-context=false \
    --wait
fi

# k3d publishes the API server on the host's loopback, which this container cannot reach.
# Join the cluster network instead. A second connect fails, so accept "already attached".
if ! docker network connect "$SHELF_NETWORK" "$SHELF_DEVCONTAINER" 2>/dev/null; then
  docker network inspect "$SHELF_NETWORK" | grep -q "\"Name\": \"${SHELF_DEVCONTAINER}\"" \
    || die "could not attach $SHELF_DEVCONTAINER to $SHELF_NETWORK"
fi

mkdir -p "$(dirname "$KUBECONFIG")"
k3d kubeconfig get "$SHELF_CLUSTER" > "$KUBECONFIG"
chmod 600 "$KUBECONFIG"

# Prefer the server's container name through Docker's embedded DNS. If it does not resolve,
# use the IP and pin TLS verification to the name, which is in the certificate as a SAN.
cluster_entry="k3d-${SHELF_CLUSTER}"
if getent hosts "$SHELF_SERVER" >/dev/null; then
  kubectl config set-cluster "$cluster_entry" --server="https://${SHELF_SERVER}:6443" >/dev/null
  echo "API server: https://${SHELF_SERVER}:6443 (resolved through Docker DNS)"
else
  ip="$(docker container inspect \
    -f "{{with index .NetworkSettings.Networks \"${SHELF_NETWORK}\"}}{{.IPAddress}}{{end}}" \
    "$SHELF_SERVER")"
  [[ -n "$ip" ]] || die "could not determine the IP of $SHELF_SERVER on $SHELF_NETWORK"
  kubectl config set-cluster "$cluster_entry" \
    --server="https://${ip}:6443" --tls-server-name="$SHELF_SERVER" >/dev/null
  echo "API server: https://${ip}:6443 ($SHELF_SERVER did not resolve; TLS pinned to that name)"
fi

kubectl wait --for=condition=Ready node --all --timeout=90s
kubectl get nodes -o wide
