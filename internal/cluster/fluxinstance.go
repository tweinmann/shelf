package cluster

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Artifact is a tagged OCI artifact: the platform, the chart, or an app's deploy artifact.
type Artifact struct {
	// URL is the repository, e.g. oci://ghcr.io/tweinmann/shelf/platform.
	URL string
	// Tag is the artifact tag, e.g. v0.3.0.
	Tag string
}

// ParseArtifact parses oci://<registry>/<repository>:<tag>. Registry and tag are required.
func ParseArtifact(ref string) (Artifact, error) {
	rest, ok := strings.CutPrefix(ref, "oci://")
	if !ok {
		return Artifact{}, fmt.Errorf("%q must start with oci://", ref)
	}
	tag, err := name.NewTag(rest, name.StrictValidation)
	if err != nil {
		return Artifact{}, fmt.Errorf("%q must be oci://<registry>/<repository>:<tag>: %w", ref, err)
	}
	return Artifact{URL: "oci://" + tag.Context().Name(), Tag: tag.TagStr()}, nil
}

func (a Artifact) String() string { return a.URL + ":" + a.Tag }

// Reference returns the artifact without the oci:// scheme, as registry clients expect it.
func (a Artifact) Reference() string { return strings.TrimPrefix(a.String(), "oci://") }

// jsonPatch is a kustomize patch for one object created by the Flux Operator.
func jsonPatch(kind, objName, patch string) map[string]any {
	return map[string]any{
		"target": map[string]any{"kind": kind, "name": objName},
		"patch":  patch,
	}
}

// syncKustomizationPatch makes the sync Kustomization wait for everything it applied, so it is
// ready only when the whole platform is, and substitutes the cluster settings into it.
const syncKustomizationPatch = `- op: add
  path: /spec/wait
  value: true
- op: add
  path: /spec/postBuild
  value:
    substituteFrom:
      - kind: ConfigMap
        name: ` + ConfigName + `
`

// FluxInstance returns the FluxInstance that installs Flux and syncs the platform. insecure
// allows a platform registry without TLS.
func FluxInstance(platform Artifact, insecure bool) *unstructured.Unstructured {
	patches := []any{jsonPatch("Kustomization", PlatformSyncName, syncKustomizationPatch)}
	if insecure {
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
				"url":      platform.URL,
				"ref":      platform.Tag,
				"path":     "./",
				"interval": "1m",
			},
			"kustomize": map[string]any{
				"patches": patches,
			},
		},
	}}
}
