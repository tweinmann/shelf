#!/bin/sh
# Remove every Docker object created for shelf development, by exact name.
#
# Runs on the host Mac and needs only the docker CLI. Nothing is matched by prefix and nothing
# is pruned, so containers, volumes and images of other projects are never touched.
#
# Usage: hack/nuke.sh [--with-shared-images] [--yes]
#   --with-shared-images  also remove the third-party images shelf pulls (devcontainer base
#                         image, k3s, k3d-tools). Only use this if they were not on this
#                         machine before, since other projects may use them.
#   --yes                 do not ask for confirmation
set -eu

cd "$(dirname "$0")/.."

with_shared_images=0
assume_yes=0
for arg in "$@"; do
  case "$arg" in
    --with-shared-images) with_shared_images=1 ;;
    --yes) assume_yes=1 ;;
    -h | --help)
      sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown argument: $arg" >&2
      exit 2
      ;;
  esac
done

# Versions come from the Dockerfile so this script never drifts from what was actually pulled.
dockerfile=.devcontainer/Dockerfile
base_image=$(sed -n 's/^FROM[[:space:]]\{1,\}\([^[:space:]]*\).*/\1/p' "$dockerfile" | head -n 1)
k3d_version=$(sed -n 's/^ARG K3D_VERSION=//p' "$dockerfile")
k3s_image=$(sed -n 's/^ENV SHELF_K3S_IMAGE=//p' "$dockerfile")
if [ -z "$base_image" ] || [ -z "$k3d_version" ] || [ -z "$k3s_image" ]; then
  echo "error: could not read versions from $dockerfile" >&2
  exit 1
fi

# Keep in sync with the footprint inventory in docs/plan.md.
containers="k3d-shelf-dev-server-0 k3d-shelf-dev-tools shelf-devcontainer"
networks="k3d-shelf-dev"
volumes="k3d-shelf-dev-images shelf-gomodcache shelf-gocache shelf-claude-config"
shared_images="$base_image $k3s_image ghcr.io/k3d-io/k3d-tools:$k3d_version"

# The devcontainer image has a generated name (vsc-shelf-<hash>...). Resolve it through the
# container rather than by name, so another project folder that is also called "shelf" is safe.
devcontainer_image=""
if docker container inspect shelf-devcontainer >/dev/null 2>&1; then
  devcontainer_image=$(docker container inspect -f '{{.Image}}' shelf-devcontainer)
fi

# Collect what exists, in removal order: containers, networks, volumes, images.
plan=""
add() {
  plan="${plan}$1 $2
"
}
for c in $containers; do
  if docker container inspect "$c" >/dev/null 2>&1; then add container "$c"; fi
done
for n in $networks; do
  if docker network inspect "$n" >/dev/null 2>&1; then add network "$n"; fi
done
for v in $volumes; do
  if docker volume inspect "$v" >/dev/null 2>&1; then add volume "$v"; fi
done
if [ -n "$devcontainer_image" ]; then add image "$devcontainer_image"; fi
if [ "$with_shared_images" -eq 1 ]; then
  for i in $shared_images; do
    if docker image inspect "$i" >/dev/null 2>&1; then add image "$i"; fi
  done
fi

if [ -z "$plan" ]; then
  echo "Nothing to remove."
else
  echo "These Docker objects will be removed:"
  printf '%s' "$plan" | sed 's/^/  /'
  if [ "$assume_yes" -ne 1 ]; then
    printf 'Proceed? [y/N] '
    read -r answer
    case "$answer" in
      y | Y | yes) ;;
      *)
        echo "Aborted."
        exit 1
        ;;
    esac
  fi
  printf '%s' "$plan" | while read -r kind name; do
    case "$kind" in
      container) docker container rm -f "$name" >/dev/null ;;
      network) docker network rm "$name" >/dev/null ;;
      volume) docker volume rm "$name" >/dev/null ;;
      image) docker image rm "$name" >/dev/null ;;
    esac
    echo "removed $kind $name"
  done
fi

echo
echo "Left in place on purpose:"
echo "  - the 'vscode' volume, shared by all VS Code devcontainers"
echo "  - Docker build cache from the devcontainer build (bounded by Docker's cache GC)"
echo "  - untagged intermediate images, if any (inspect with: docker images -f dangling=true)"
if [ "$with_shared_images" -ne 1 ]; then
  for i in $shared_images; do
    if docker image inspect "$i" >/dev/null 2>&1; then
      echo "  - shared image $i (pass --with-shared-images if it was not here before)"
    fi
  done
fi
if [ -z "$devcontainer_image" ]; then
  # Listing only. Nothing here is deleted, because the name alone does not prove ownership.
  candidates=$(docker images --format '{{.Repository}}:{{.Tag}}' | grep '^vsc-shelf-' || true)
  if [ -n "$candidates" ]; then
    echo "  - devcontainer images whose container is already gone; check and remove by hand:"
    printf '%s\n' "$candidates" | sed 's/^/      /'
  fi
fi
