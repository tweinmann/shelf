# Shared helpers for hack/ scripts that run inside the devcontainer. Source, do not execute.

readonly SHELF_CLUSTER=shelf-dev
readonly SHELF_KUBECONFIG=/home/vscode/.kube/shelf-dev.yaml

die() {
  echo "error: $*" >&2
  exit 1
}

# Refuse to run anywhere but inside the shelf devcontainer, against its own Docker daemon, with
# the dev kubeconfig selected. This keeps every script away from the host's Docker daemon, the
# host's kubeconfig and other clusters.
require_devcontainer() {
  [[ "${SHELF_DEVCONTAINER:-}" == 1 ]] \
    || die "run this inside the shelf devcontainer (SHELF_DEVCONTAINER is not set)"
  [[ "${KUBECONFIG:-}" == "$SHELF_KUBECONFIG" ]] \
    || die "KUBECONFIG must be $SHELF_KUBECONFIG, got '${KUBECONFIG:-}'"
  # The docker-in-docker daemon runs in this container and reports its host name. A daemon
  # reached through a mounted host socket would report the Docker Desktop VM instead.
  local daemon
  daemon="$(docker info --format '{{.Name}}' 2>/dev/null)" \
    || die "no Docker daemon reachable; is the docker-in-docker daemon running?"
  [[ "$daemon" == "$(hostname)" ]] \
    || die "docker talks to daemon '$daemon', not to the one in this container ($(hostname))"
}
