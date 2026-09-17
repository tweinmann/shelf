package cluster

import (
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/tweinmann/shelf/internal/testutil"
)

func TestFluxOperatorObjects(t *testing.T) {
	objs, err := FluxOperatorObjects()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, obj := range objs {
		kinds[obj.GetKind()]++
	}
	if kinds["CustomResourceDefinition"] == 0 || kinds["Deployment"] != 1 || kinds["Namespace"] != 1 {
		t.Fatalf("unexpected objects in the operator manifest: %v", kinds)
	}

	var images []string
	for _, obj := range objs {
		if obj.GetKind() != "Deployment" {
			continue
		}
		containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
		for _, c := range containers {
			images = append(images, c.(map[string]any)["image"].(string))
		}
	}
	want := "ghcr.io/controlplaneio-fluxcd/flux-operator:" + FluxOperatorVersion
	if len(images) != 1 || images[0] != want {
		t.Errorf("operator image %v, want %s: FluxOperatorVersion and the manifest disagree", images, want)
	}

	crds := map[string]bool{}
	for _, obj := range objs {
		if obj.GetKind() == "CustomResourceDefinition" {
			crds[obj.GetName()] = true
		}
	}
	if !crds["fluxinstances.fluxcd.controlplane.io"] {
		t.Error("the manifest lacks the FluxInstance CRD")
	}
}

func TestInstallOrder(t *testing.T) {
	obj := func(kind string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetKind(kind)
		return u
	}
	first, rest := installOrder([]*unstructured.Unstructured{
		obj("ServiceAccount"), obj("CustomResourceDefinition"), obj("Namespace"), obj("Deployment"),
	})
	kinds := func(objs []*unstructured.Unstructured) string {
		var s []string
		for _, o := range objs {
			s = append(s, o.GetKind())
		}
		return strings.Join(s, ",")
	}
	if got := kinds(first); got != "CustomResourceDefinition,Namespace" {
		t.Errorf("first = %s", got)
	}
	if got := kinds(rest); got != "ServiceAccount,Deployment" {
		t.Errorf("rest = %s", got)
	}
}

func TestDecodeObjects(t *testing.T) {
	objs, err := decodeObjects([]byte("---\n# comment only\n---\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: a\n"))
	if err != nil || len(objs) != 1 {
		t.Fatalf("got %d objects, %v", len(objs), err)
	}
	if _, err := decodeObjects([]byte("apiVersion: v1\nkind: Namespace\n")); err == nil {
		t.Error("an object without a name must be rejected")
	}
}

func TestParseArtifact(t *testing.T) {
	tests := []struct {
		ref     string
		url     string
		tag     string
		wantErr string
	}{
		{ref: "oci://ghcr.io/tweinmann/shelf/platform:v0.3.0", url: "oci://ghcr.io/tweinmann/shelf/platform", tag: "v0.3.0"},
		{ref: "oci://shelf-registry:5000/shelf/platform:dev", url: "oci://shelf-registry:5000/shelf/platform", tag: "dev"},
		{ref: "ghcr.io/tweinmann/shelf/platform:v1", wantErr: "must start with oci://"},
		{ref: "oci://ghcr.io/tweinmann/shelf/platform", wantErr: "<tag>"},
		{ref: "oci://ghcr.io/tweinmann/shelf/platform@sha256:" + strings.Repeat("0", 64), wantErr: "<tag>"},
		{ref: "oci://platform:v1", wantErr: "<tag>"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			p, err := ParseArtifact(tt.ref)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.URL != tt.url || p.Tag != tt.tag {
				t.Errorf("got %s %s, want %s %s", p.URL, p.Tag, tt.url, tt.tag)
			}
			if p.String() != tt.ref {
				t.Errorf("String() = %s, want %s", p, tt.ref)
			}
			if p.Reference() != strings.TrimPrefix(tt.ref, "oci://") {
				t.Errorf("Reference() = %s", p.Reference())
			}
		})
	}
}

func TestFluxInstance(t *testing.T) {
	for _, insecure := range []bool{false, true} {
		name := "fluxinstance"
		ref := "oci://ghcr.io/tweinmann/shelf/platform:v0.3.0"
		if insecure {
			name, ref = "fluxinstance-insecure", "oci://shelf-registry:5000/shelf/platform:dev"
		}
		t.Run(name, func(t *testing.T) {
			p, err := ParseArtifact(ref)
			if err != nil {
				t.Fatal(err)
			}
			out, err := yaml.Marshal(FluxInstance(p, insecure).Object)
			if err != nil {
				t.Fatal(err)
			}
			testutil.Golden(t, filepath.Join("testdata", name+".yaml"), out)
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		before, after string
		want          Action
	}{
		{"", "7", Created},
		{"7", "7", Unchanged},
		{"7", "9", Configured},
	}
	for _, tt := range tests {
		if got := classify(tt.before, tt.after); got != tt.want {
			t.Errorf("classify(%q, %q) = %s, want %s", tt.before, tt.after, got, tt.want)
		}
	}
}

func TestReadyCondition(t *testing.T) {
	// Decoded like objects from the API server, whose numbers are int64.
	obj := func(src string) *unstructured.Unstructured {
		data, err := yaml.YAMLToJSON([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		u := &unstructured.Unstructured{}
		if err := u.UnmarshalJSON(data); err != nil {
			t.Fatal(err)
		}
		return u
	}
	const head = "apiVersion: v1\nkind: X\nmetadata:\n  name: x\n  generation: 2\n"
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{"no status", head, false},
		{"no Ready condition", head + "status:\n  observedGeneration: 2\n  conditions:\n  - {type: Reconciling, status: 'True'}\n", false},
		{"ready", head + "status:\n  observedGeneration: 2\n  conditions:\n  - {type: Ready, status: 'True', message: done}\n", true},
		{"not ready", head + "status:\n  observedGeneration: 2\n  conditions:\n  - {type: Ready, status: 'False'}\n", false},
		{"ready for an older generation", head + "status:\n  observedGeneration: 1\n  conditions:\n  - {type: Ready, status: 'True'}\n", false},
		{"generation on the condition", head + "status:\n  conditions:\n  - {type: Ready, status: 'True', observedGeneration: 2}\n", true},
		{"no generation at all", head + "status:\n  conditions:\n  - {type: Ready, status: 'True'}\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := readyCondition(obj(tt.src))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("ready = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlatformObjects(t *testing.T) {
	chart, err := ParseArtifact("oci://shelf-registry:5000/shelf/charts/shelf-app:0.0.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	objs := ConfigObjects(Settings{Domain: "dev.local", Chart: chart, InsecureRegistry: true})
	withLogin, err := RegistrySecret(&RegistryAuth{Username: "tobi", Token: "not-a-real-token"})
	if err != nil {
		t.Fatal(err)
	}
	withoutLogin, err := RegistrySecret(nil)
	if err != nil {
		t.Fatal(err)
	}
	app, err := ParseArtifact("oci://ghcr.io/tweinmann/hello-deploy:main")
	if err != nil {
		t.Fatal(err)
	}
	objs = append(objs, withLogin, withoutLogin,
		AppSecret("hello", map[string]string{"db-password": "not-a-real-password"}),
		AppSecret("empty", nil),
		AppProvider(AppOptions{Name: "hello", Artifact: app}))

	var out []byte
	for _, obj := range objs {
		data, err := yaml.Marshal(obj.Object)
		if err != nil {
			t.Fatal(err)
		}
		out = append(append(out, "---\n"...), data...)
	}
	testutil.Golden(t, filepath.Join("testdata", "objects.yaml"), out)
}

func TestFailOnAuthError(t *testing.T) {
	obj := func(status, message string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetKind("OCIRepository")
		u.SetName("deploy")
		u.SetGeneration(1)
		_ = unstructured.SetNestedField(u.Object, int64(1), "status", "observedGeneration")
		_ = unstructured.SetNestedSlice(u.Object, []any{
			map[string]any{"type": "Ready", "status": status, "message": message},
		}, "status", "conditions")
		return u
	}
	denied := "failed to determine artifact digest: GET https://ghcr.io/token?scope=x: DENIED: denied"
	tests := []struct {
		name    string
		obj     *unstructured.Unstructured
		wantErr bool
	}{
		{"login refused", obj("False", denied), true},
		{"not found yet", obj("False", "failed to pull artifact: manifest unknown"), false},
		{"ready", obj("True", "stored artifact"), false},
		// A ready object whose message mentions a past failure must not fail the wait.
		{"ready after a refusal", obj("True", denied), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := failOnAuthError(readyCondition)(tt.obj)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error %v, want error %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "shelf init cluster") {
				t.Errorf("the error does not say what to do: %v", err)
			}
		})
	}
}
