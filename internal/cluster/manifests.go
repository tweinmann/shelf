// Package cluster installs the shelf platform into the cluster of a kubeconfig: the Flux
// Operator, and a FluxInstance that installs Flux and syncs the platform artifact. It uses
// only the Kubernetes API, so it works against any cluster.
package cluster

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// Pinned versions. Update the operator with `just flux-operator-update <version>`.
const (
	FluxOperatorVersion = "v0.60.0"
	FluxVersion         = "2.9.5"
)

// Names of the objects shelf creates or waits for.
const (
	FluxNamespace    = "flux-system"
	FluxInstanceName = "flux"
	// PlatformSyncName is the name the Flux Operator gives the OCIRepository and the
	// Kustomization it creates for spec.sync.
	PlatformSyncName = "flux-system"
	// FieldManager owns the fields shelf applies with server-side apply.
	FieldManager = "shelf"
)

// fluxOperatorManifest is the install.yaml of the Flux Operator release FluxOperatorVersion,
// unchanged.
//
//go:embed manifests/flux-operator.yaml
var fluxOperatorManifest []byte

// FluxOperatorObjects returns the objects of the embedded Flux Operator manifest.
func FluxOperatorObjects() ([]*unstructured.Unstructured, error) {
	objs, err := decodeObjects(fluxOperatorManifest)
	if err != nil {
		return nil, fmt.Errorf("flux operator manifest: %w", err)
	}
	return objs, nil
}

func decodeObjects(data []byte) ([]*unstructured.Unstructured, error) {
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var objs []*unstructured.Unstructured
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return objs, nil
			}
			return nil, err
		}
		if len(m) == 0 {
			continue
		}
		obj := &unstructured.Unstructured{Object: m}
		if obj.GetKind() == "" || obj.GetName() == "" {
			return nil, fmt.Errorf("object %d has no kind or name", len(objs)+1)
		}
		objs = append(objs, obj)
	}
}

// installOrder splits objects into those that others depend on (namespaces and CRDs), and the
// rest, keeping the manifest order within each group.
func installOrder(objs []*unstructured.Unstructured) (first, rest []*unstructured.Unstructured) {
	for _, obj := range objs {
		switch obj.GetKind() {
		case "Namespace", "CustomResourceDefinition":
			first = append(first, obj)
		default:
			rest = append(rest, obj)
		}
	}
	return first, rest
}
