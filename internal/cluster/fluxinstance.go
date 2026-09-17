package cluster

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Platform is the OCI artifact Flux syncs into the cluster.
type Platform struct {
	// URL is the repository, e.g. oci://ghcr.io/tweinmann/shelf/platform.
	URL string
	// Tag is the artifact tag, e.g. v0.3.0.
	Tag string
	// Insecure allows a registry without TLS, such as the dev registry.
	Insecure bool
}

// ParsePlatform parses oci://<registry>/<repository>:<tag>. Registry and tag are required.
func ParsePlatform(ref string, insecure bool) (Platform, error) {
	rest, ok := strings.CutPrefix(ref, "oci://")
	if !ok {
		return Platform{}, fmt.Errorf("platform %q must start with oci://", ref)
	}
	tag, err := name.NewTag(rest, name.StrictValidation)
	if err != nil {
		return Platform{}, fmt.Errorf("platform %q must be oci://<registry>/<repository>:<tag>: %w", ref, err)
	}
	return Platform{
		URL:      "oci://" + tag.Context().Name(),
		Tag:      tag.TagStr(),
		Insecure: insecure,
	}, nil
}

func (p Platform) String() string { return p.URL + ":" + p.Tag }

// jsonPatch is a kustomize patch for one object created by the Flux Operator.
func jsonPatch(kind, objName, patch string) map[string]any {
	return map[string]any{
		"target": map[string]any{"kind": kind, "name": objName},
		"patch":  patch,
	}
}

// FluxInstance returns the FluxInstance that installs Flux and syncs the platform.
func FluxInstance(p Platform) *unstructured.Unstructured {
	patches := []any{
		// The sync Kustomization is ready only once everything it applied is ready, including
		// HelmReleases, so waiting for it covers the whole platform.
		jsonPatch("Kustomization", PlatformSyncName, "- op: add\n  path: /spec/wait\n  value: true\n"),
	}
	if p.Insecure {
		// spec.sync has no field for registries without TLS.
		patches = append(patches,
			jsonPatch("OCIRepository", PlatformSyncName, "- op: add\n  path: /spec/insecure\n  value: true\n"))
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "fluxcd.controlplane.io/v1",
		"kind":       "FluxInstance",
		"metadata": map[string]any{
			"name":      FluxInstanceName,
			"namespace": FluxNamespace,
		},
		"spec": map[string]any{
			"distribution": map[string]any{
				"version":  FluxVersion,
				"registry": "ghcr.io/fluxcd",
			},
			"components": []any{
				"source-controller",
				"kustomize-controller",
				"helm-controller",
				"notification-controller",
			},
			"cluster": map[string]any{
				"type":          "kubernetes",
				"networkPolicy": true,
			},
			"sync": map[string]any{
				"kind":     "OCIRepository",
				"url":      p.URL,
				"ref":      p.Tag,
				"path":     "./",
				"interval": "1m",
			},
			"kustomize": map[string]any{
				"patches": patches,
			},
		},
	}}
}
