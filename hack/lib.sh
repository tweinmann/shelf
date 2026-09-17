# Shared helpers for hack/ scripts that run inside the devcontainer. Source, do not execute.

readonly SHELF_CLUSTER=shelf-dev
readonly SHELF_KUBECONFIG=/home/vscode/.kube/shelf-dev.yaml
# The dev registry for platform, chart and deploy artifacts, created with the cluster. Pods and
# this container both reach it as shelf-registry:5000 (plain HTTP), so one reference works for
# pushing, for shelf and for Flux.
readonly SHELF_REGISTRY=shelf-registry
readonly SHELF_REGISTRY_PORT=5000
readonly SHELF_REGISTRY_HOST="$SHELF_REGISTRY:$SHELF_REGISTRY_PORT"

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

# create_namespace <name>: creates a namespace and waits for its default ServiceAccount, which
# Kubernetes adds a moment later; a pod created before that is rejected.
create_namespace() {
  kubectl create namespace "$1" >/dev/null
  kubectl -n "$1" wait --for=create serviceaccount/default --timeout=60s >/dev/null
}
