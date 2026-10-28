#!/usr/bin/env bash
# Checks the Phase 11 verification chain against the dev cluster, which verifies with the dev
# key (.local/cosign) as a production cluster verifies keyless signatures:
#   - an app whose signed and scanned artifact names a signed built image is verified and may be
#     exposed;
#   - the admission policy admits that image and denies an unsigned one, in the app's namespace
#     and in a preview's;
#   - a preview of a signed branch artifact is verified;
#   - a signed artifact without a scan summary runs, but is not verified and may not be exposed;
#   - an unsigned artifact is not applied: the wait fails at the deploy artifact with a hint, and
#     what ran keeps running;
#   - shelf app rm takes the app's ClusterImagePolicy with it.
# It prints the policy-controller's RAM. The exposure gate itself is tested at level 1, since
# only smoke-quick may pass --quick. Installs or updates the platform first (idempotent). Needs
# network access for images.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
app=verify-smoke
# As on GHCR: the deploy artifact is <registry>/<package>, a built image <package>/<component>.
artifact="oci://$SHELF_REGISTRY_HOST/smoke/$app"
image="$SHELF_REGISTRY_HOST/smoke/$app/web"
export SHELF_HOME="$work/home"

shelf() { "$work/shelf" "$@"; }

cleanup() {
  if [[ "${KEEP:-}" == 1 ]]; then
    echo "KEEP=1: leaving app $app in place"
    return
  fi
  shelf app rm "$app" --yes >/dev/null 2>&1 || true
  rm -rf "$work"
}

step() { echo "--- $*"; }

# commit: a random 40-digit commit hash, as the reusable workflow stamps on an artifact.
commit() { head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# render <dir> <image>: writes the deploy manifest of a one-component app built as image. shelf
# render looks the image up over TLS, which the dev registry has not, so it renders the Docker
# Hub image the dev ones were copied from, and the reference is swapped afterwards.
render() {
  mkdir -p "$1"
  local base
  base="traefik/whoami:v1.11.0@$(crane digest traefik/whoami:v1.11.0)"
  shelf render "$work/app.yaml" --image "web=$base" 2>/dev/null | sed "s|$base|$2|" >"$1/configmap.yaml"
  grep -qF "$2" "$1/configmap.yaml" || die "the rendered manifest does not name $2"
}

# verification <jq filter> [preview]: whether the app's (or a preview's) verification matches.
verification() {
  local args=(app status "$app" --json)
  [[ -n "${2:-}" ]] && args+=(--preview "$2")
  shelf "${args[@]}" | jq -e ".verification | $1" >/dev/null
}

# admitted <namespace> <image>: whether the admission webhook lets a pod with image start there.
# A server-side dry run goes through the webhooks without creating anything.
admitted() {
  kubectl -n "$1" run "admission-check" --image="$2" --restart=Never --dry-run=server >/dev/null 2>"$work/admission.txt"
}

for leftover in "namespace/$app" "-n shelf-system resourcesetinputprovider/$app" "clusterimagepolicy/shelf-$app"; do
  # shellcheck disable=SC2086
  kubectl get $leftover >/dev/null 2>&1 && die "$leftover already exists; remove it first (shelf app rm $app)"
done
trap cleanup EXIT

step "build shelf, push platform and chart, init cluster with the dev key"
(cd "$repo" && go build -o "$work/shelf" ./cmd/shelf)
"$repo/hack/platform-push.sh" >/dev/null 2>&1
"$repo/hack/chart-push.sh" >/dev/null 2>&1
shelf init cluster --yes --signer-key "$SHELF_SIGNER_PUB" --host-suffix "$(cluster_host_suffix)" --insecure-registry \
  --platform "oci://$SHELF_REGISTRY_HOST/shelf/platform:dev" \
  --chart "oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev" | grep -E '^  signer'
kubectl -n cosign-system wait helmrelease/policy-controller --for=condition=Ready --timeout=300s >/dev/null \
  || die "the policy-controller is not ready"

step "a signed and an unsigned built image of the app's package"
crane copy --insecure --platform linux/arm64 traefik/whoami:v1.11.0 "$image:base" >/dev/null 2>&1 \
  || die "cannot copy the base image"
for kind in signed unsigned; do
  crane mutate --insecure -l "dev.shelf.smoke=$kind-$(commit)" "$image:base" -t "$image:$kind" >/dev/null 2>&1 \
    || die "cannot make the $kind image"
done
sign_artifact "$image:signed"
signed="$image@$(crane digest --insecure "$image:signed")"
unsigned="$image@$(crane digest --insecure "$image:unsigned")"

cat >"$work/app.yaml" <<'EOF'
apiVersion: shelf.dev/v1alpha1
name: verify-smoke
components:
  web:
    build: ./web
    port: 80
    route: /
    resources: { cpu: 10m, memory: 16Mi }
EOF
render "$work/signed" "$signed"

step "a signed and scanned artifact is verified and may be exposed"
first="$(commit)"
push_artifact "$artifact:main" "$work/signed" "main@sha1:$first" >/dev/null
shelf app add "$app" "$artifact:main" --insecure-registry >/dev/null
shelf app status "$app" --wait --commit "$first" --timeout 5m --json >/dev/null || die "the signed app did not become Ready"
verification '.verified and .exposable and (.scan | length) == 1 and .scan[0].scanned' \
  || die "the signed app is not verified: $(shelf app status "$app" --json | jq -c .verification)"
[[ "$(kubectl get clusterimagepolicy "shelf-$app" -o jsonpath='{.spec.images[0].glob}')" == "$SHELF_REGISTRY_HOST/smoke/$app/**" ]] \
  || die "the app has no ClusterImagePolicy for its package"

step "the admission policy admits the signed image and denies the unsigned one"
admitted "$app" "$signed" || die "the signed image was denied: $(cat "$work/admission.txt")"
admitted "$app" "$unsigned" && die "the unsigned image was admitted"
grep -q 'policy.sigstore.dev' "$work/admission.txt" || die "the unsigned image was not denied by the policy: $(cat "$work/admission.txt")"
admitted "$app" "postgres:16" || die "a third-party image was denied: $(cat "$work/admission.txt")"

step "a preview of a signed branch artifact is verified, and its namespace is checked too"
shelf app credentials "$app" --previews=1 >/dev/null
branch="$(commit)"
push_artifact "$artifact:branch-check" "$work/signed" "check@sha1:$branch" >/dev/null
shelf app status "$app" --preview check --wait --commit "$branch" --timeout 5m --json >/dev/null \
  || die "the preview did not become Ready"
verification '.verified and (.exposable | not)' check || die "the preview is not verified, or counts as exposable"
admitted "$app--check" "$unsigned" && die "the unsigned image was admitted in the preview"
shelf app credentials "$app" --no-previews >/dev/null

step "a signed artifact without a scan runs, but is not verified"
noscan="$(commit)"
flux push artifact "$artifact:staging" --path "$work/signed" --source local --revision "main@sha1:$noscan" \
  --insecure-registry >/dev/null 2>&1 || die "push failed"
sign_artifact "${artifact#oci://}:staging"
crane tag --insecure "${artifact#oci://}:staging" main >/dev/null 2>&1 || die "tag failed"
shelf app status "$app" --wait --commit "$noscan" --timeout 5m --json >/dev/null || die "the artifact without a scan did not roll out"
verification '(.verified | not) and (.exposable | not) and (.reason | test("no scan"))' \
  || die "an artifact without a scan counts as verified: $(shelf app status "$app" --json | jq -c .verification)"

step "an unsigned artifact is not applied, and what runs keeps running"
bad="$(commit)"
flux push artifact "$artifact:main" --path "$work/signed" --source local --revision "main@sha1:$bad" \
  --annotations "dev.shelf.scan=$(dev_scan "$work/signed")" --insecure-registry >/dev/null 2>&1 || die "push failed"
code=0
shelf app status "$app" --wait --commit "$bad" --timeout 3m --json >"$work/unsigned.json" || code=$?
[[ "$code" == 1 ]] || die "the wait for an unsigned artifact ended with $code, not 1 (Failed)"
jq -e '.stage.name == "the deploy artifact" and (.stage.hint | test("id-token: write"))' "$work/unsigned.json" >/dev/null \
  || die "the verdict does not name the signature: $(jq -c .stage "$work/unsigned.json")"
shelf app status "$app" --json | jq -e ".commit == \"$noscan\" and (.stages[-1].phase == \"Ready\")" >/dev/null \
  || die "the app no longer runs the last verified artifact"

step "the policy-controller's RAM"
kubectl top pod -n cosign-system --no-headers 2>/dev/null | awk '{print "  " $1 ": " $3}' \
  || echo "  (metrics not available yet)"

step "shelf app rm takes the image policy with it"
shelf app rm "$app" --yes >/dev/null
kubectl get clusterimagepolicy "shelf-$app" >/dev/null 2>&1 && die "the ClusterImagePolicy is still there"
echo "smoke-verify passed"
