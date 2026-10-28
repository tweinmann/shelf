#!/usr/bin/env bash
# Prints the level-2 smoke tests a change needs, as a JSON array for the CI matrix. Reads the
# changed paths from stdin, one per line. Runs on the CI runner, not in the devcontainer.
#
# A path that is documentation or planning needs none. A path listed below needs the tests
# named for it. Anything else needs all of them: an unlisted path is a reason to test, never a
# reason to skip.
set -euo pipefail

all=(smoke-secrets smoke-chart smoke-init smoke-apps smoke-agent smoke-previews)
declare -A need=()

for_path() {
  case "$1" in
    # Planning, documentation and examples (the examples are validated by `just test`).
    openspec/* | *.md | LICENSE | NOTICE | examples/* | .claude/* | schema/*) ;;
    # Workflows that are checked by running them, not by the smoke tests.
    .github/workflows/build.yml | .github/workflows/release.yml) ;;
    # Code no smoke test reaches: the admin UI and the APIs of Cloudflare and GitHub.
    internal/server/* | internal/cloudflare/* | internal/github/*) ;;
    hack/smoke/secret-expansion*) echo smoke-secrets ;;
    hack/smoke/chart.sh) echo smoke-chart ;;
    charts/* | internal/chart/*) echo smoke-chart smoke-apps smoke-agent smoke-previews ;;
    hack/smoke/init.sh | internal/cli/init.go | internal/cli/init_test.go) echo smoke-init ;;
    hack/smoke/apps.sh) echo smoke-apps ;;
    hack/smoke/agent.* | internal/cli/json* | internal/cli/open* | internal/cli/wait* | internal/cli/tenant_test.go)
      echo smoke-agent ;;
    hack/smoke/previews.sh | internal/cli/previews* | internal/ops/previews*) echo smoke-previews ;;
    internal/cli/* | internal/ops/* | internal/progress/* | internal/secrets/* | internal/hostcfg/*)
      echo smoke-apps smoke-agent smoke-previews ;;
    *) echo "${all[@]}" ;;
  esac
}

while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  for smoke in $(for_path "$path"); do
    need[$smoke]=1
  done
done

out=()
for smoke in "${all[@]}"; do
  [[ -n "${need[$smoke]:-}" ]] && out+=("\"$smoke\"")
done
(IFS=,; echo "[${out[*]}]")
