#!/bin/sh
# Remove every Docker object created for shelf development, by exact name.
#
# Runs on the host Mac and needs only the docker CLI. Nothing is matched by prefix and nothing
# is pruned, so containers, volumes and images of other projects are never touched. The k3d
# cluster lives in the devcontainer's own Docker daemon, whose data is in the shelf-docker and
# shelf-containerd volumes, so it disappears with them.
#
# Usage: hack/nuke.sh [--with-shared-images] [--yes]
#   --with-shared-images  also remove the devcontainer base image. Only use this if it was not
#                         on this machine before, since other projects may use it.
#   --yes                 do not ask for confirmation
set -eu

cd "$(dirname "$0")/.."

# Inside the devcontainer, docker talks to the container's own daemon, and removing
# shelf-devcontainer from the host would kill this very script.
if [ -f /.dockerenv ] || [ -n "${SHELF_DEVCONTAINER:-}" ]; then
  echo "error: run this in a terminal on the host Mac, not inside a container" >&2
  exit 1
fi

with_shared_images=0
assume_yes=0
for arg in "$@"; do
  case "$arg" in
    --with-shared-images) with_shared_images=1 ;;
    --yes) assume_yes=1 ;;
    -h | --help)
      sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown argument: $arg" >&2
      exit 2
      ;;
  esac
done

# The base image comes from the Dockerfile so this script never drifts from what was pulled.
dockerfile=.devcontainer/Dockerfile
base_image=$(sed -n 's/^FROM[[:space:]]\{1,\}\([^[:space:]]*\).*/\1/p' "$dockerfile" | head -n 1)
if [ -z "$base_image" ]; then
  echo "error: could not read the base image from $dockerfile" >&2
  exit 1
fi

# Keep in sync with the footprint inventory in docs/plan.md and the mounts in
# .devcontainer/devcontainer.json.
container=shelf-devcontainer
volumes="shelf-gomodcache shelf-gocache shelf-claude-config shelf-docker shelf-containerd"
shared_images="$base_image"

# The devcontainer image has a generated name (vsc-shelf-<hash>...). Resolve it through the
# container rather than by name, so another project folder that is also called "shelf" is safe.
# The volumes behind the docker-in-docker daemon are resolved the same way, in case the
# feature mounted them under its own generated names. Only these two mount targets count:
# the container also mounts the shared 'vscode' volume, which must stay.
devcontainer_image=""
if docker container inspect "$container" >/dev/null 2>&1; then
  devcontainer_image=$(docker container inspect -f '{{.Image}}' "$container")
  mounted=$(docker container inspect -f \
    '{{range .Mounts}}{{if eq .Type "volume"}}{{.Destination}}={{.Name}} {{end}}{{end}}' "$container")
  for m in $mounted; do
    case "$m" in
      /var/lib/docker=* | /var/lib/containerd=*) v=${m#*=} ;;
      *) continue ;;
    esac
    case " $volumes " in
      *" $v "*) ;;
      *) volumes="$volumes $v" ;;
    esac
  done
fi

# Collect what exists, in removal order: container, volumes, images.
plan=""
add() {
  plan="${plan}$1 $2
"
}
if docker container inspect "$container" >/dev/null 2>&1; then add container "$container"; fi
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
      # -v removes the container's anonymous volumes and never touches named volumes.
      container) docker container rm -f -v "$name" >/dev/null ;;
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
