#!/usr/bin/env bash
# Checks the Phase 12 hardening against the dev cluster:
#   - the isolation, from the app's namespace and from a preview's: the app's own service is
#     reachable, another namespace, the Kubernetes API and a private address (the dev registry's
#     container, standing in for the LAN) are not, the internet is; another namespace cannot reach
#     in, Traefik can (shelf app open);
#   - a LAN exception lets the app reach that address, and never its preview;
#   - Pod Security refuses a privileged pod, runs postgres:16, and lists it as violating
#     restricted;
#   - a passing test gives Ready; a failing test on the app ends the wait with Failed at "the tests"
#     and keeps the previous version; a failing test on a preview keeps the preview on it, red;
#   - shelf app report for that preview names the changed component.
# It prints the time from push to Ready with a test against the same app without one. Installs or
# updates the platform first (idempotent). Needs network access for images.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
require_devcontainer

repo="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
app=hardening-smoke
other=hardening-smoke-other
# As on GHCR: the deploy artifact is <registry>/<package>, a built image <package>/<component>.
artifact="oci://$SHELF_REGISTRY_HOST/smoke/$app"
image="$SHELF_REGISTRY_HOST/smoke/$app/web"
export SHELF_HOME="$work/home"

shelf() { "$work/shelf" "$@"; }

cleanup() {
  if [[ "${KEEP:-}" == 1 ]]; then
    echo "KEEP=1: leaving app $app and namespace $other in place"
    return
  fi
  [[ -n "${open_pid:-}" ]] && kill "$open_pid" 2>/dev/null || true
  shelf app rm "$app" --yes >/dev/null 2>&1 || true
  kubectl delete namespace "$other" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  rm -rf "$work"
}

step() { echo "--- $*"; }

# commit: a random 40-digit commit hash, as the reusable workflow stamps on an artifact.
commit() { head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# render <dir> <web image> <test args>: writes the deploy manifest of the app with a web image
# built for it, postgres:16, and, unless the args are empty, a curl test. shelf render looks the
# built image up over TLS, which the dev registry has not, so it renders the Docker Hub image
# the dev one was copied from, and the reference is swapped afterwards.
render() {
  local dir="$1" web="$2" test="$3" base
  mkdir -p "$dir"
  base="traefik/whoami:v1.11.0@$(crane digest traefik/whoami:v1.11.0)"
  {
    cat <<'EOF'
apiVersion: shelf.dev/v1alpha1
name: hardening-smoke
components:
  web:
    build: ./web
    port: 80
    route: /
    health: { path: /health }
    resources: { cpu: 10m, memory: 16Mi }
  db:
    image: postgres:16
    port: 5432
    env:
      POSTGRES_PASSWORD: ${secrets.pw}
    resources: { cpu: 50m, memory: 128Mi }
secrets:
  pw: { generate: true }
EOF
    if [[ -n "$test" ]]; then
      printf 'tests:\n  smoke:\n    image: curlimages/curl:8.10.1\n    args: [%s]\n    resources: { cpu: 10m, memory: 16Mi }\n' "$test"
    fi
  } >"$work/app.yaml"
  shelf render "$work/app.yaml" --image "web=$base" 2>/dev/null | sed "s|$base|$web|" >"$dir/configmap.yaml"
  grep -qF "$web" "$dir/configmap.yaml" || die "the rendered manifest does not name $web"
}

# probe <namespace> <url>: whether a pod in that namespace gets an answer from url. The pod is
# subject to the namespace's NetworkPolicy like every other one there.
probe() {
  kubectl -n "$1" exec probe -- wget -q -T 4 -O /dev/null "$2" >/dev/null 2>&1
}

# probe_pod <namespace>: starts the pod probe uses.
probe_pod() {
  # The namespace warns that busybox would break restricted; that is expected, and noise here.
  kubectl -n "$1" run probe --image=busybox:1.36 --restart=Never -- sleep 3600 >/dev/null 2>&1
  kubectl -n "$1" wait pod/probe --for=condition=Ready --timeout=120s >/dev/null || die "the probe in $1 did not start"
}

# fenced <namespace> <what>: the isolation as seen from a namespace of the app or a preview.
fenced() {
  local ns="$1" what="$2"
  probe "$ns" "http://web.$ns" || die "$what: its own service is not reachable"
  probe "$ns" "http://other.$other" && die "$what: another namespace is reachable"
  kubectl -n "$ns" exec probe -- nc -z -w 4 kubernetes.default.svc 443 >/dev/null 2>&1 \
    && die "$what: the Kubernetes API is reachable"
  probe "$ns" "http://$registry_ip:5000/v2/" && die "$what: the private address $registry_ip is reachable"
  probe "$ns" "http://example.com" || die "$what: the internet is not reachable"
  kubectl -n "$other" exec probe -- wget -q -T 4 -O /dev/null "http://web.$ns" >/dev/null 2>&1 \
    && die "$what: another namespace reaches in"
  return 0
}

# wait_for <commit> [args...]: waits for the app (or, with --preview, a preview) to run a commit,
# and prints how long it took; the exit status is the verdict's.
wait_for() {
  local sha="$1"
  shift
  shelf app status "$app" --wait --commit "$sha" --timeout 6m --json "$@" >"$work/verdict.json"
}

for leftover in "namespace/$app" "namespace/$other" "-n shelf-system resourcesetinputprovider/$app"; do
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
  --chart "oci://$SHELF_REGISTRY_HOST/shelf/charts/shelf-app:0.0.0-dev" >/dev/null
kubectl -n cosign-system wait helmrelease/policy-controller --for=condition=Ready --timeout=300s >/dev/null \
  || die "the policy-controller is not ready"
# The registry's own address on the cluster's network stands in for a LAN device: here it is
# 127.0.0.1 through /etc/hosts, which inside a pod is the pod itself.
registry_ip="$(docker inspect -f "{{with index .NetworkSettings.Networks \"k3d-$SHELF_CLUSTER\"}}{{.IPAddress}}{{end}}" "$SHELF_REGISTRY")"
[[ "$registry_ip" =~ ^(10|172|192)\. ]] || die "cannot find the dev registry on the cluster's network: $registry_ip"

step "signed web images for main and for a branch, and another namespace to reach for"
crane copy --insecure --platform linux/arm64 traefik/whoami:v1.11.0 "$image:base" >/dev/null 2>&1 \
  || die "cannot copy the base image"
for kind in main branch; do
  crane mutate --insecure -l "dev.shelf.smoke=$kind-$(commit)" "$image:base" -t "$image:$kind" >/dev/null 2>&1 \
    || die "cannot make the $kind image"
  sign_artifact "$image:$kind"
done
web_main="$image@$(crane digest --insecure "$image:main")"
web_branch="$image@$(crane digest --insecure "$image:branch")"
create_namespace "$other"
kubectl -n "$other" run other --image=traefik/whoami:v1.11.0 --port=80 --expose >/dev/null
probe_pod "$other"

step "the app without a test, then with one: push to Ready"
render "$work/plain" "$web_main" ""
first="$(commit)"
push_artifact "$artifact:main" "$work/plain" "main@sha1:$first" >/dev/null
shelf app add "$app" "$artifact:main" --insecure-registry >/dev/null
wait_for "$first" || die "the app without a test did not become Ready: $(jq -c .stage "$work/verdict.json")"
again="$(commit)"
start=$(date +%s)
push_artifact "$artifact:main" "$work/plain" "main@sha1:$again" >/dev/null
wait_for "$again" || die "the app without a test did not become Ready again: $(jq -c .stage "$work/verdict.json")"
plain_seconds=$(($(date +%s) - start))
render "$work/tested" "$web_main" '-fsS, --retry, "5", --retry-connrefused, --retry-delay, "1", "http://${web.host}:${web.port}/"'
second="$(commit)"
start=$(date +%s)
push_artifact "$artifact:main" "$work/tested" "main@sha1:$second" >/dev/null
wait_for "$second" || die "the app with a passing test did not become Ready: $(jq -c .stage "$work/verdict.json")"
tested_seconds=$(($(date +%s) - start))
shelf app status "$app" --json | jq -e '.tests == [{"name": "smoke", "phase": "Ready"}] and .stages[-1].name == "the tests"' >/dev/null \
  || die "the passed test is not in the status: $(shelf app status "$app" --json | jq -c '{tests, stage: .stages[-1]}')"

step "Pod Security: postgres runs, is listed for restricted, a privileged pod is refused"
shelf app status "$app" --json | jq -e '.isolation.podSecurity == "baseline" and
  any(.isolation.restrictedViolations[]; .pod | startswith("db-"))' >/dev/null \
  || die "postgres is not listed as violating restricted: $(shelf app status "$app" --json | jq -c .isolation)"
kubectl -n "$app" run privileged --image=busybox:1.36 --restart=Never --overrides='{"spec":{"containers":[{"name":"privileged","image":"busybox:1.36","command":["sleep","60"],"securityContext":{"privileged":true}}]}}' \
  >/dev/null 2>"$work/privileged.txt" && die "a privileged pod was admitted"
grep -q 'violates PodSecurity "baseline' "$work/privileged.txt" || die "the privileged pod was not refused by Pod Security: $(cat "$work/privileged.txt")"

step "the isolation of the app, and Traefik reaching in"
probe_pod "$app"
fenced "$app" "the app"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
# The binary itself, not the function shelf: $! has to be its PID, or kill leaves it running.
"$work/shelf" app open "$app" --port "$port" >"$work/open.txt" 2>&1 &
open_pid=$!
for _ in $(seq 1 30); do curl -fsS -o /dev/null "http://127.0.0.1:$port/" 2>/dev/null && break; sleep 1; done
curl -fsS -o /dev/null "http://127.0.0.1:$port/" || die "Traefik does not reach the app: $(cat "$work/open.txt")"
kill "$open_pid"
wait "$open_pid" 2>/dev/null || true
open_pid=""

step "a LAN exception reaches its range, and the app's preview does not get it"
shelf app credentials "$app" --allow-lan "$registry_ip/32" --previews=1 >/dev/null
probe "$app" "http://$registry_ip:5000/v2/" || die "the app does not reach its LAN exception $registry_ip"
render "$work/branch" "$web_branch" '-fsS, --retry, "5", --retry-connrefused, --retry-delay, "1", "http://${web.host}:${web.port}/"'
branch="$(commit)"
push_artifact "$artifact:branch-check" "$work/branch" "check@sha1:$branch" >/dev/null
wait_for "$branch" --preview check || die "the preview did not become Ready: $(jq -c .stage "$work/verdict.json")"
probe_pod "$app--check"
fenced "$app--check" "the preview"
probe "$app--check" "http://web.$app" && die "the preview reaches the app"

step "a failing test on a preview keeps the preview on it, red"
render "$work/branch-broken" "$web_branch" '-fsS, --max-time, "5", "http://${web.host}:81/"'
broken_branch="$(commit)"
push_artifact "$artifact:branch-check" "$work/branch-broken" "check@sha1:$broken_branch" >/dev/null
code=0
wait_for "$broken_branch" --preview check || code=$?
[[ "$code" == 1 ]] || die "the wait for a preview with a failing test ended with $code, not 1"
jq -e '.stage.name == "the tests" and (.stage.message | test("exit code 7"))' "$work/verdict.json" >/dev/null \
  || die "the verdict does not name the test: $(jq -c .stage "$work/verdict.json")"
release="$(kubectl -n "$app--check" get helmrelease "$app--check" -o json)"
jq -e '.status.history[0].configDigest == .status.lastAttemptedConfigDigest' <<<"$release" >/dev/null \
  || die "the preview does not run the version whose test failed"

step "shelf app report names the changed component"
shelf app report "$app" --preview check --json | jq -e '.comparison.comparable and
  any(.comparison.changed[]; .component == "web") and any(.tests[]; .name == "smoke" and .phase == "Failed")' >/dev/null \
  || die "the report does not name web as changed: $(shelf app report "$app" --preview check --json | jq -c '{comparison, tests}')"
shelf app report "$app" --preview check | grep -q '| web | changed |' || die "the Markdown report lacks the changed web image"

step "a failing test on the app keeps the previous version"
digest_before="$(kubectl -n "$app" get helmrelease "$app" -o jsonpath='{.status.history[0].configDigest}')"
render "$work/broken" "$web_main" '-fsS, --max-time, "5", "http://${web.host}:81/"'
broken="$(commit)"
push_artifact "$artifact:main" "$work/broken" "main@sha1:$broken" >/dev/null
code=0
wait_for "$broken" || code=$?
[[ "$code" == 1 ]] || die "the wait for a failing test ended with $code, not 1"
jq -e '.stage.name == "the tests"' "$work/verdict.json" >/dev/null \
  || die "the verdict does not name the tests: $(jq -c .stage "$work/verdict.json")"
for _ in $(seq 1 90); do
  [[ "$(kubectl -n "$app" get helmrelease "$app" -o jsonpath='{.status.history[0].configDigest}')" == "$digest_before" ]] && break
  sleep 4
done
[[ "$(kubectl -n "$app" get helmrelease "$app" -o jsonpath='{.status.history[0].configDigest}')" == "$digest_before" ]] \
  || die "the app did not go back to the version whose test passed"

step "push to Ready"
echo "  without a test  ${plain_seconds}s"
echo "  with a test     ${tested_seconds}s"

step "shelf app rm"
shelf app rm "$app" --yes >/dev/null
echo "smoke-hardening passed"
