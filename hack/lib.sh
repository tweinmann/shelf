# Shared helpers for hack/ scripts that run inside the devcontainer. Source, do not execute.

readonly SHELF_CLUSTER=shelf-dev
readonly SHELF_DEVCONTAINER=shelf-devcontainer
readonly SHELF_SERVER="k3d-${SHELF_CLUSTER}-server-0"
readonly SHELF_NETWORK="k3d-${SHELF_CLUSTER}"
readonly SHELF_KUBECONFIG=/home/vscode/.kube/shelf-dev.yaml

die() {
  echo "error: $*" >&2
  exit 1
}

# Refuse to run anywhere but inside the shelf devcontainer, with the dev kubeconfig selected.
# This keeps every script away from the host's kubeconfig and from other clusters.
require_devcontainer() {
  [[ -n "${LOCAL_WORKSPACE_FOLDER:-}" ]] \
    || die "run this inside the shelf devcontainer (LOCAL_WORKSPACE_FOLDER is not set)"
  [[ "${KUBECONFIG:-}" == "$SHELF_KUBECONFIG" ]] \
    || die "KUBECONFIG must be $SHELF_KUBECONFIG, got '${KUBECONFIG:-}'"
  local own_hostname
  own_hostname="$(docker container inspect -f '{{.Config.Hostname}}' "$SHELF_DEVCONTAINER" 2>/dev/null)" \
    || die "container $SHELF_DEVCONTAINER not found on the host daemon"
  [[ "$own_hostname" == "$(hostname)" ]] \
    || die "this shell is not running in $SHELF_DEVCONTAINER"
}
