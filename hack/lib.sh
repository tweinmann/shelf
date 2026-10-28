# Shared helpers for hack/ scripts that run inside the devcontainer. Source, do not execute.

readonly SHELF_CLUSTER=shelf-dev
readonly SHELF_KUBECONFIG=/home/vscode/.kube/shelf-dev.yaml
# The dev registry for platform, chart, deploy artifacts and built images, created with the
# cluster. Pods and this container both reach it as shelf-registry.local:5000 (plain HTTP), so one
# reference works for pushing, for shelf and for Flux. The .local name matters: the Sigstore
# policy-controller has no setting for a registry without TLS, but go-containerregistry speaks
# HTTP to a .local host.
readonly SHELF_REGISTRY=shelf-registry.local
readonly SHELF_REGISTRY_PORT=5000
readonly SHELF_REGISTRY_HOST="$SHELF_REGISTRY:$SHELF_REGISTRY_PORT"
# The dev signer: a cosign key pair without a password, made once by cluster-up, which signs
# every artifact and image pushed to the dev registry. The dev cluster is installed with its
# public half (--signer-key) and so verifies like a production cluster, with a key instead of
# keyless signatures. The key is machine-local (.local is gitignored) and signs dev artifacts
# only; nothing it signs is recorded in a transparency log.
SHELF_SIGNER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.local/cosign"
readonly SHELF_SIGNER_DIR
readonly SHELF_SIGNER_KEY="$SHELF_SIGNER_DIR/cosign.key"
readonly SHELF_SIGNER_PUB="$SHELF_SIGNER_DIR/cosign.pub"

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

# cluster_host_suffix prints the host suffix the cluster was installed with, empty for none or
# for a cluster shelf has not set up. The smoke tests install the platform again with it: a
# different suffix would give every app with a domain another name, which shelf refuses without
# --move-hosts.
cluster_host_suffix() {
  kubectl -n flux-system get configmap shelf-config \
    -o jsonpath='{.data.SHELF_HOST_SUFFIX}' 2>/dev/null || true
}

# create_namespace <name>: creates a namespace and waits for its default ServiceAccount, which
# Kubernetes adds a moment later; a pod created before that is rejected.
create_namespace() {
  kubectl create namespace "$1" >/dev/null
  kubectl -n "$1" wait --for=create serviceaccount/default --timeout=60s >/dev/null
}

# ensure_signer_key creates the dev key pair unless it exists.
ensure_signer_key() {
  [[ -f "$SHELF_SIGNER_KEY" && -f "$SHELF_SIGNER_PUB" ]] && return 0
  mkdir -p "$SHELF_SIGNER_DIR"
  chmod 0700 "$SHELF_SIGNER_DIR"
  (cd "$SHELF_SIGNER_DIR" && COSIGN_PASSWORD='' cosign generate-key-pair >/dev/null 2>&1) \
    || die "cosign could not create the dev key pair in $SHELF_SIGNER_DIR"
  echo "dev signer: key pair created in $SHELF_SIGNER_DIR"
}

# sign_artifact <reference>: signs an artifact or image in the dev registry by digest with the
# dev key, in cosign's classic format, which both Flux and the policy-controller read, and without
# a transparency log entry. The reference has a tag or a digest and no oci:// scheme.
sign_artifact() {
  [[ -f "$SHELF_SIGNER_KEY" ]] || die "no dev signer key; run just cluster-up"
  local ref="${1#oci://}" repo digest
  if [[ "$ref" == *@* ]]; then
    repo="${ref%@*}"
  else
    repo="${ref%:*}"
  fi
  digest="$(crane digest --insecure "$ref")" || die "cannot read the digest of $ref"
  COSIGN_PASSWORD='' cosign sign --key "$SHELF_SIGNER_KEY" --new-bundle-format=false \
    --use-signing-config=false --tlog-upload=false --allow-http-registry --allow-insecure-registry \
    -y "$repo@$digest" >/dev/null 2>&1 || die "cosign could not sign $ref"
}

# dev_scan <dir>: the dev.shelf.scan summary for the deploy artifact built from dir, which
# names every image digest its ConfigMap pins. Nothing scans in the dev registry, and the
# scanner says so; the summary only lets a dev app count as verified.
dev_scan() {
  grep -rhoE '@sha256:[0-9a-f]{64}' "$1" | sort -u | cut -c2- |
    jq -Rnc --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      '{scanner: "none (dev registry)", scannedAt: $t, images: ([inputs | {(.): {critical: 0, high: 0}}] | add // {})}'
}

# push_artifact <oci://registry/repo:tag> <dir> <revision> [flux push args...]: pushes a deploy
# artifact with a scan summary and signs it, as the reusable workflow does, and prints its digest.
# Like the workflow, it pushes under another tag (staging) first and moves the tag only to the
# signed artifact, so that Flux never reads it unsigned.
push_artifact() {
  local ref="$1" dir="$2" revision="$3"
  shift 3
  local repo="${ref%:*}" digest
  digest="$(flux push artifact "$repo:staging" --path "$dir" --source local --revision "$revision" \
    --annotations "dev.shelf.scan=$(dev_scan "$dir")" --insecure-registry --output json "$@" 2>/dev/null |
    jq -r .digest)" || die "push of $ref failed"
  [[ -n "$digest" && "$digest" != null ]] || die "push of $ref failed"
  sign_artifact "${repo#oci://}@$digest"
  # flux tag artifact has no flag for a registry without TLS.
  crane tag --insecure "${repo#oci://}:staging" "${ref##*:}" >/dev/null 2>&1 \
    || die "tagging $ref failed"
  echo "$digest"
}
