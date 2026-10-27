package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestConfigDigest pins the digest to one helm-controller wrote: testdata/configdigest holds the
// values ConfigMap and the HelmRelease of an app on the dev cluster (2026-10-27), with the
// configDigest of its release. If a Flux update changes how helm-controller hashes values, this
// test fails before any wait waits forever.
func TestConfigDigest(t *testing.T) {
	t.Parallel()
	values, err := os.ReadFile(filepath.Join("testdata", "configdigest", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", "configdigest", "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	var release struct {
		Values       map[string]any `json:"values"`
		ConfigDigest string         `json:"configDigest"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		t.Fatal(err)
	}
	got, err := ConfigDigest(string(values), release.Values)
	if err != nil {
		t.Fatal(err)
	}
	if got != release.ConfigDigest {
		t.Errorf("digest %s, helm-controller wrote %s", got, release.ConfigDigest)
	}
	other, _ := ConfigDigest(string(values)+"\nextra: 1\n", release.Values)
	if other == got {
		t.Error("other values have the same digest")
	}
}

func TestMergeValues(t *testing.T) {
	t.Parallel()
	got := mergeValues(
		map[string]any{"name": "agent", "platform": map[string]any{"domain": "x", "keep": true}, "list": []any{1}},
		map[string]any{"name": "app", "platform": map[string]any{"domain": "shelf.internal"}, "list": []any{2}},
	)
	want := `{"list":[2],"name":"app","platform":{"domain":"shelf.internal","keep":true}}`
	if b, _ := json.Marshal(got); string(b) != want {
		t.Errorf("merged %s, want %s", b, want)
	}
}

func TestRollout(t *testing.T) {
	t.Parallel()
	objects := healthy()
	_ = unstructured.SetNestedMap(objects.source.Object, map[string]any{
		"revision": "main@sha256:new",
		"metadata": map[string]any{revisionAnnotation: "main@sha1:42a964eb0b4bd7248305d5f22233d6dbabcd56f3"},
	}, "status", "artifact")
	_ = unstructured.SetNestedField(objects.kustomization.Object, "main@sha256:old", "status", "lastAppliedRevision")
	_ = unstructured.SetNestedField(objects.kustomization.Object, "main@sha256:new", "status", "lastAttemptedRevision")
	_ = unstructured.SetNestedMap(objects.release.Object, map[string]any{"name": "shop"}, "spec", "values")
	_ = unstructured.SetNestedField(objects.release.Object, "sha256:attempted", "status", "lastAttemptedConfigDigest")
	_ = unstructured.SetNestedSlice(objects.release.Object, []any{
		map[string]any{"version": int64(3), "configDigest": "sha256:released"},
		map[string]any{"version": int64(2), "configDigest": "sha256:older"},
	}, "status", "history")
	conditions, _, _ := unstructured.NestedSlice(objects.release.Object, "status", "conditions")
	conditions = append(conditions, map[string]any{"type": "Reconciling", "status": "True", "reason": "Progressing"})
	_ = unstructured.SetNestedSlice(objects.release.Object, conditions, "status", "conditions")
	values := &unstructured.Unstructured{Object: map[string]any{"data": map[string]any{"app.yaml": "name: shop-pkg\n"}}}

	r := rollout("shop", objects, values)
	if r.Revision != "main@sha256:new" || r.AppliedRevision != "main@sha256:old" || r.AttemptedRevision != "main@sha256:new" ||
		r.Commit != "42a964eb0b4bd7248305d5f22233d6dbabcd56f3" {
		t.Errorf("revisions %+v", r)
	}
	want, _ := ConfigDigest("name: shop-pkg\n", map[string]any{"name": "shop"})
	if r.ValuesDigest != want || r.ReleasedDigest != "sha256:released" || r.AttemptedDigest != "sha256:attempted" {
		t.Errorf("digests values %s (want %s), released %s, attempted %s", r.ValuesDigest, want, r.ReleasedDigest, r.AttemptedDigest)
	}
	if !r.Reconciling {
		t.Error("a release with Reconciling=True is not reconciling")
	}
	if len(r.Diagnosis.Stages) != 5 {
		t.Errorf("stages %d", len(r.Diagnosis.Stages))
	}

	if r := rollout("shop", healthy(), nil); r.ValuesDigest != "" || r.Commit != "" || r.Reconciling {
		t.Errorf("an app without values or annotations %+v", r)
	}
}
