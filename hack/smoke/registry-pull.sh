#!/usr/bin/env bash
# Checks that the dev cluster can pull a private GHCR image through an imagePullSecret.
#
# Usage: hack/smoke/registry-pull.sh ghcr.io/<owner>/<image>:<tag>
# GHCR_USERNAME and GHCR_TOKEN are read from the environment, or prompted for. The token must be
# a classic PAT with the read:packages scope; fine-grained tokens cannot pull from GHCR.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

image="${1:-}"
[[ "$image" == ghcr.io/* ]] || die "usage: $0 ghcr.io/<owner>/<image>:<tag>"

if [[ -z "${GHCR_USERNAME:-}" ]]; then read -rp "GHCR username: " GHCR_USERNAME; fi
if [[ -z "${GHCR_TOKEN:-}" ]]; then
  read -rsp "GHCR token (classic PAT, read:packages): " GHCR_TOKEN
  echo
fi

ns=shelf-smoke-registry
pod=registry-pull
trap 'kubectl delete namespace "$ns" --ignore-not-found --wait=false >/dev/null' EXIT

create_namespace "$ns"

# The credentials only travel through shell builtins and stdin, never through command arguments,
# so they do not show up in a process listing.
auth="$(printf '%s:%s' "$GHCR_USERNAME" "$GHCR_TOKEN" | base64 -w0)"
dockerconfig="$(printf '{"auths":{"ghcr.io":{"username":"%s","password":"%s","auth":"%s"}}}' \
  "$GHCR_USERNAME" "$GHCR_TOKEN" "$auth" | base64 -w0)"

kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ghcr-pull
  namespace: $ns
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: $dockerconfig
---
apiVersion: v1
kind: Pod
metadata:
  name: $pod
  namespace: $ns
spec:
  restartPolicy: Never
  imagePullSecrets:
    - name: ghcr-pull
  containers:
    - name: app
      image: $image
EOF

# The image ID is set once the pull succeeded, whatever the container does afterwards.
for _ in $(seq 90); do
  image_id="$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.status.containerStatuses[0].imageID}')"
  reason="$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.status.containerStatuses[0].state.waiting.reason}')"
  if [[ -n "$image_id" ]]; then
    echo "PASS: pulled $image_id"
    exit 0
  fi
  if [[ "$reason" == ErrImagePull || "$reason" == ImagePullBackOff ]]; then
    kubectl -n "$ns" describe pod "$pod" | sed -n '/^Events:/,$p'
    die "image pull failed ($reason)"
  fi
  sleep 2
done
die "timed out waiting for the image pull"
