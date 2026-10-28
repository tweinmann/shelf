package cluster

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestPreviewsResourceSet pins what a preview must and must not get from the platform.
func TestPreviewsResourceSet(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../platform/previews/resourceset.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Only what is applied counts, not what the comments say about it.
	s := regexp.MustCompile(`(?m)^\s*#.*$`).ReplaceAllString(string(data), "")
	for _, want := range []string{
		"key: shelf.dev/previews", "kind: ResourceQuota", "kind: LimitRange", "preview: true",
		"interval: 30s", "domain: shelf.internal", "shelf.dev/preview: ", "kind: HelmRelease",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("platform/previews/resourceset.yaml lacks %q", want)
		}
	}
	// A preview is never exposed.
	for _, never := range []string{"cloudflared", "tunnel", "copyFrom: shelf-system/app-"} {
		if strings.Contains(s, never) {
			t.Errorf("platform/previews/resourceset.yaml mentions %q", never)
		}
	}
	// Flux substitutes $... when it applies the platform; only ${SHELF_*} may survive that.
	if m := regexp.MustCompile(`\$[A-Za-z{]`).FindAllString(s, -1); len(m) != strings.Count(s, "${SHELF_") {
		t.Errorf("a $ other than ${SHELF_...}: %v", m)
	}
}

func TestPreviewProvider(t *testing.T) {
	t.Parallel()
	p := PreviewProvider(AppOptions{Name: "hello", Artifact: Artifact{URL: "oci://ghcr.io/acme/hello", Tag: "main"},
		Registry: "ghcr", Previews: 3})
	if p.GetName() != "hello--previews" || p.GetNamespace() != SystemNamespace {
		t.Errorf("name %s/%s", p.GetNamespace(), p.GetName())
	}
	if labels := p.GetLabels(); labels[PreviewsLabel] != "hello" || labels[AppLabel] != "" {
		t.Errorf("labels %v: the provider of previews must not look like an app", labels)
	}
	if p.GetAnnotations()["fluxcd.controlplane.io/reconcileEvery"] != "30s" {
		t.Errorf("annotations %v", p.GetAnnotations())
	}
	spec, _, _ := unstructured.NestedMap(p.Object, "spec")
	if spec["type"] != "OCIArtifactTag" || spec["url"] != "oci://ghcr.io/acme/hello" {
		t.Errorf("spec %v", spec)
	}
	if include, _, _ := unstructured.NestedString(p.Object, "spec", "filter", "includeTag"); include != "^branch-" {
		t.Errorf("includeTag %q", include)
	}
	if secret, _, _ := unstructured.NestedString(p.Object, "spec", "secretRef", "name"); secret != "connection-registry-ghcr" {
		t.Errorf("secretRef %q", secret)
	}
	if previewLimit(p) != 3 || previewLimit(nil) != 0 {
		t.Errorf("limit %d", previewLimit(p))
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(p.Object, "spec", "insecure"); found {
		t.Error("insecure is set for a registry with TLS")
	}

	// The dev registry: no login, no TLS.
	dev := PreviewProvider(AppOptions{Name: "hello", Artifact: Artifact{URL: "oci://shelf-registry:5000/hello"},
		Insecure: true, Previews: 1})
	if insecure, _, _ := unstructured.NestedBool(dev.Object, "spec", "insecure"); !insecure {
		t.Error("insecure is not set for the dev registry")
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(dev.Object, "spec", "secretRef"); found {
		t.Error("a provider without a registry connection has a secretRef")
	}
}

func TestPreviewState(t *testing.T) {
	t.Parallel()
	o := healthy()
	o.provider, o.previewOf, o.slug = nil, "hello", "feature-login"
	_ = unstructured.SetNestedMap(o.source.Object, map[string]any{"url": "oci://ghcr.io/acme/hello",
		"ref": map[string]any{"tag": "branch-feature-login"}}, "spec")
	_ = unstructured.SetNestedMap(o.source.Object, map[string]any{"revision": "branch-feature-login@sha256:abc",
		"metadata": map[string]any{revisionAnnotation: "feature/login@sha1:42a964eb0b4bd7248305d5f22233d6dbabcd56f3"}},
		"status", "artifact")
	if o.isApp() {
		t.Fatal("a preview is listed as an app")
	}
	s := previewState("hello--feature-login", o)
	if s.Name != "hello--feature-login" || s.Artifact.Tag != "branch-feature-login" || s.Phase != PhaseReady ||
		s.Commit != "42a964eb0b4bd7248305d5f22233d6dbabcd56f3" {
		t.Errorf("state %+v", s)
	}
	o.pods = []*unstructured.Unstructured{pod("Pending", container(false, waiting("ImagePullBackOff", "")))}
	if s := previewState("hello--feature-login", o); s.Phase != PhaseFailed || s.Stage != "the pods" {
		t.Errorf("failing preview %+v", s)
	}
}
