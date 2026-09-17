# Development image for shelf. Every tool version is pinned here; this file is the single
# source of truth for versions (hack/nuke.sh reads the base image from it). The Docker engine
# comes from the docker-in-docker feature, pinned in devcontainer.json.
FROM mcr.microsoft.com/devcontainers/go:2-1.27-trixie

ARG K3D_VERSION=5.9.0
ARG KUBECTL_VERSION=1.36.4
ARG HELM_VERSION=4.3.0
ARG FLUX_VERSION=2.9.5
ARG JUST_VERSION=1.58.0
ARG KUBECONFORM_VERSION=0.8.0

# k3s image for the dev cluster. Matches the k3s stable channel; kubectl follows its minor version.
ENV SHELF_K3S_IMAGE=rancher/k3s:v1.36.4-k3s1

RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      arm64) just_arch=aarch64 ;; \
      amd64) just_arch=x86_64 ;; \
      *) echo "unsupported architecture: $arch" >&2; exit 1 ;; \
    esac; \
    tmp="$(mktemp -d)"; \
    curl -fsSL -o /usr/local/bin/k3d \
      "https://github.com/k3d-io/k3d/releases/download/v${K3D_VERSION}/k3d-linux-${arch}"; \
    curl -fsSL -o /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/linux/${arch}/kubectl"; \
    curl -fsSL "https://get.helm.sh/helm-v${HELM_VERSION}-linux-${arch}.tar.gz" \
      | tar -xz -C "$tmp"; \
    mv "$tmp/linux-${arch}/helm" /usr/local/bin/helm; \
    curl -fsSL "https://github.com/fluxcd/flux2/releases/download/v${FLUX_VERSION}/flux_${FLUX_VERSION}_linux_${arch}.tar.gz" \
      | tar -xz -C /usr/local/bin flux; \
    curl -fsSL "https://github.com/casey/just/releases/download/${JUST_VERSION}/just-${JUST_VERSION}-${just_arch}-unknown-linux-musl.tar.gz" \
      | tar -xz -C /usr/local/bin just; \
    curl -fsSL "https://github.com/yannh/kubeconform/releases/download/v${KUBECONFORM_VERSION}/kubeconform-linux-${arch}.tar.gz" \
      | tar -xz -C /usr/local/bin kubeconform; \
    chmod 0755 /usr/local/bin/k3d /usr/local/bin/kubectl /usr/local/bin/helm \
      /usr/local/bin/flux /usr/local/bin/just /usr/local/bin/kubeconform; \
    rm -rf "$tmp"

# A named volume copies ownership and mode from the image directory it is mounted over, but only
# if that directory exists in the image. Without this, the cache and config volumes end up owned
# by root and the vscode user cannot write to them.
# The Go directories are world-writable with the sticky bit, like /go in the base image, rather
# than owned by vscode: in CI, devcontainers/ci changes vscode's UID to the runner's, and usermod
# only re-owns the home directory. /go/pkg needs this too, because mkdir creates it as root and Go
# keeps its checksum database cache in /go/pkg/sumdb.
RUN mkdir -p /go/pkg/mod /home/vscode/.cache/go-build /home/vscode/.claude /home/vscode/.kube \
 && chown -R vscode:vscode /home/vscode/.cache /home/vscode/.claude /home/vscode/.kube \
 && chown vscode:golang /go/pkg /go/pkg/mod \
 && chmod 1777 /go/pkg /go/pkg/mod
