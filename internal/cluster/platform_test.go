package cluster

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigsyaml "sigs.k8s.io/yaml"
)

// platformObjects renders a ResourceSet of the platform the way the cluster does: Flux first
// substitutes ${SHELF_...} from the cluster settings into the YAML of the whole object, then the
// Flux Operator renders << >> per input set, both the resources and the resourcesTemplate.
func platformObjects(t *testing.T, file string, settings Settings, inputs map[string]any) []*unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	// kustomize-controller marshals the object again before it substitutes, so the
	// substitution sees the YAML as Flux writes it, not as the file spells it.
	encoded, err := yaml.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]string
	for _, o := range ConfigObjects(settings) {
		if o.GetKind() == "ConfigMap" {
			data, _, _ = unstructured.NestedStringMap(o.Object, "data")
		}
	}
	substituted := regexp.MustCompile(`\$\{([A-Z_]+)(?::=([^}]*))?\}`).ReplaceAllStringFunc(string(encoded), func(m string) string {
		parts := regexp.MustCompile(`\$\{([A-Z_]+)(?::=([^}]*))?\}`).FindStringSubmatch(m)
		if v, ok := data[parts[1]]; ok && v != "" {
			return v
		}
		return parts[2]
	})
	var rs map[string]any
	if err := yaml.Unmarshal([]byte(substituted), &rs); err != nil {
		t.Fatalf("%s after substitution: %v", file, err)
	}
	spec := rs["spec"].(map[string]any)

	render := func(text string) []byte {
		tmpl, err := template.New(file).Delims("<<", ">>").Funcs(template.FuncMap{
			"trimPrefix": func(prefix, s string) string { return strings.TrimPrefix(s, prefix) },
			"inputs":     func() map[string]any { return inputs },
		}).Option("missingkey=zero").Parse(text)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out bytes.Buffer
		if err := tmpl.Execute(&out, nil); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		return out.Bytes()
	}
	var objs []*unstructured.Unstructured
	add := func(doc []byte) {
		if len(bytes.TrimSpace(doc)) == 0 {
			return
		}
		u := &unstructured.Unstructured{}
		if err := sigsyaml.Unmarshal(doc, &u.Object); err != nil {
			t.Fatalf("%s: rendered object does not parse: %v\n%s", file, err, doc)
		}
		if len(u.Object) > 0 {
			objs = append(objs, u)
		}
	}
	if resources, ok := spec["resources"].([]any); ok {
		for _, r := range resources {
			doc, err := yaml.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			add(render(string(doc)))
		}
	}
	if tmpl, ok := spec["resourcesTemplate"].(string); ok {
		for _, doc := range strings.Split(string(render(tmpl)), "\n---") {
			add([]byte(doc))
		}
	}
	return objs
}

func find(objs []*unstructured.Unstructured, kind, name string) *unstructured.Unstructured {
	for _, o := range objs {
		if o.GetKind() == kind && o.GetName() == name {
			return o
		}
	}
	return nil
}

// TestPlatformVerifies renders the ResourceSets apps and previews with both kinds of signer and
// checks that everything they fetch is verified, that their namespaces are checked by the
// policy-controller, and that each app gets the policy for its built images.
func TestPlatformVerifies(t *testing.T) {
	t.Parallel()
	chart := Artifact{URL: "oci://ghcr.io/tweinmann/shelf/charts/shelf-app", Tag: "0.8.0"}
	signers := map[string]Signer{"keyless": DefaultSigner(), "key": {Key: testSignerKey(t)}}
	for mode, signer := range signers {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			settings := Settings{Chart: chart, Signer: signer}
			app := platformObjects(t, "../../platform/apps/resourceset.yaml", settings, map[string]any{
				"name": "hello", "url": "oci://ghcr.io/acme/hello", "tag": "main", "insecure": false,
				"domain": "", "tunnel": "", "quick": false, "registry": "ghcr", "cloudflare": "",
			})
			preview := platformObjects(t, "../../platform/previews/resourceset.yaml", settings, map[string]any{
				"app": "hello", "url": "oci://ghcr.io/acme/hello", "tag": "branch-feature-login",
				"insecure": false, "registry": "ghcr",
			})
			for name, objs := range map[string][]*unstructured.Unstructured{"app": app, "preview": preview} {
				for _, repo := range []string{"deploy", "shelf-app"} {
					o := find(objs, "OCIRepository", repo)
					if o == nil {
						t.Fatalf("%s: no OCIRepository %s", name, repo)
					}
					verify, _, _ := unstructured.NestedMap(o.Object, "spec", "verify")
					if verify["provider"] != "cosign" {
						t.Errorf("%s: OCIRepository %s verify %v", name, repo, verify)
					}
					if (verify["secretRef"] != nil) != (mode == "key") || (verify["matchOIDCIdentity"] != nil) != (mode == "keyless") {
						t.Errorf("%s: OCIRepository %s verify %v for a %s signer", name, repo, verify, mode)
					}
				}
				var ns *unstructured.Unstructured
				for _, o := range objs {
					if o.GetKind() == "Namespace" {
						ns = o
					}
				}
				if ns == nil || ns.GetLabels()["policy.sigstore.dev/include"] != "true" {
					t.Errorf("%s: namespace %v is not checked by the policy-controller", name, ns)
				}
				secret := find(objs, "Secret", SignerSecretName)
				if secret == nil || secret.GetAnnotations()["fluxcd.controlplane.io/copyFrom"] != SystemNamespace+"/"+signerKeySecret {
					t.Errorf("%s: no copy of the signer key: %v", name, secret)
				}
			}
			policy := find(app, "ClusterImagePolicy", "shelf-hello")
			if policy == nil {
				t.Fatal("no ClusterImagePolicy shelf-hello")
			}
			images, _, _ := unstructured.NestedSlice(policy.Object, "spec", "images")
			authorities, _, _ := unstructured.NestedSlice(policy.Object, "spec", "authorities")
			if len(images) != 1 || images[0].(map[string]any)["glob"] != "ghcr.io/acme/hello/**" {
				t.Errorf("images %v", images)
			}
			if len(authorities) != 1 || authorities[0].(map[string]any)[map[string]string{"key": "key", "keyless": "keyless"}[mode]] == nil {
				t.Errorf("authorities %v for a %s signer", authorities, mode)
			}
			if find(preview, "ClusterImagePolicy", "shelf-hello") != nil {
				t.Error("a preview has a policy of its own; the app's covers its images")
			}
		})
	}
}
