# Development tasks. Every recipe runs inside the devcontainer.
# The one host-side task, removing all shelf Docker objects, is hack/nuke.sh: just is not
# installed on the host Mac.

set shell := ["bash", "-euo", "pipefail", "-c"]

# List available recipes
default:
    @just --list

# Create or start the k3d dev cluster and write the kubeconfig
cluster-up:
    hack/cluster-up.sh

# Stop the dev cluster to free memory in the Docker Desktop VM
cluster-stop:
    k3d cluster stop shelf-dev

# Delete the dev cluster
cluster-down:
    hack/cluster-down.sh

# Delete and recreate the dev cluster
cluster-reset: cluster-down cluster-up

# Smoke test: $(VAR) expansion from secretKeyRef in env and args
smoke-secrets:
    hack/smoke/secret-expansion.sh

# Smoke test: pull a private GHCR image through an imagePullSecret
smoke-registry image:
    hack/smoke/registry-pull.sh {{image}}
