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

// TestPlatformIsolates renders an app without and with LAN exceptions, an app with a tunnel, and
// a preview of an app with exceptions, and checks the Pod Security labels, the NetworkPolicy and
// how a failing test is treated.
func TestPlatformIsolates(t *testing.T) {
	t.Parallel()
	settings := Settings{Chart: Artifact{URL: "oci://ghcr.io/tweinmann/shelf/charts/shelf-app", Tag: "0.9.0"}, Signer: DefaultSigner()}
	appInputs := func(extra map[string]any) map[string]any {
		in := map[string]any{
			"name": "hello", "url": "oci://ghcr.io/acme/hello", "tag": "main", "insecure": false,
			"domain": "", "tunnel": "", "quick": false, "registry": "ghcr", "cloudflare": "",
		}
		for k, v := range extra {
			in[k] = v
		}
		return in
	}
	// A provider from before LAN exceptions has no input lan.
	plain := platformObjects(t, "../../platform/apps/resourceset.yaml", settings, appInputs(nil))
	nas := platformObjects(t, "../../platform/apps/resourceset.yaml", settings, appInputs(map[string]any{
		"lan": []any{"192.168.1.10/32", "192.168.2.0/24"},
	}))
	tunnel := platformObjects(t, "../../platform/apps/resourceset.yaml", settings, appInputs(map[string]any{
		"domain": "example.com", "tunnel": "t-1", "cloudflare": "home", "lan": []any{},
	}))
	preview := platformObjects(t, "../../platform/previews/resourceset.yaml", settings, map[string]any{
		"app": "hello", "url": "oci://ghcr.io/acme/hello", "tag": "branch-feature-login",
		"insecure": false, "registry": "ghcr", "lan": []any{"192.168.1.10/32"},
	})

	// The extra ipBlocks of the rule that reaches the internet.
	exceptions := func(name string, objs []*unstructured.Unstructured) []string {
		t.Helper()
		policy := find(objs, "NetworkPolicy", "shelf-isolation")
		if policy == nil {
			t.Fatalf("%s: no NetworkPolicy shelf-isolation", name)
		}
		selector, _, _ := unstructured.NestedMap(policy.Object, "spec", "podSelector")
		types, _, _ := unstructured.NestedStringSlice(policy.Object, "spec", "policyTypes")
		if len(selector) != 0 || strings.Join(types, ",") != "Ingress,Egress" {
			t.Errorf("%s: the policy selects %v for %v, want every pod both ways", name, selector, types)
		}
		ingress, _, _ := unstructured.NestedSlice(policy.Object, "spec", "ingress")
		if from := ingress[0].(map[string]any)["from"].([]any); len(ingress) != 1 || len(from) != 2 {
			t.Errorf("%s: ingress %v, want the namespace and Traefik only", name, ingress)
		}
		egress, _, _ := unstructured.NestedSlice(policy.Object, "spec", "egress")
		if len(egress) != 3 {
			t.Fatalf("%s: egress %v, want namespace, DNS and internet", name, egress)
		}
		blocks := egress[2].(map[string]any)["to"].([]any)
		v4 := blocks[0].(map[string]any)["ipBlock"].(map[string]any)
		if v4["cidr"] != "0.0.0.0/0" || len(v4["except"].([]any)) != 5 {
			t.Errorf("%s: internet rule %v", name, v4)
		}
		var out []string
		for _, b := range blocks[2:] {
			out = append(out, b.(map[string]any)["ipBlock"].(map[string]any)["cidr"].(string))
		}
		return out
	}
	if got := exceptions("app", plain); len(got) != 0 {
		t.Errorf("an app without exceptions reaches %v", got)
	}
	if got := exceptions("app with a NAS", nas); strings.Join(got, ",") != "192.168.1.10/32,192.168.2.0/24" {
		t.Errorf("an app with exceptions reaches %v", got)
	}
	if got := exceptions("preview", preview); len(got) != 0 {
		t.Errorf("a preview reaches %v, want no exceptions", got)
	}

	// Only cloudflared reaches Traefik, and only where it runs.
	if find(plain, "NetworkPolicy", "shelf-cloudflared") != nil || find(preview, "NetworkPolicy", "shelf-cloudflared") != nil {
		t.Error("a policy for cloudflared without cloudflared")
	}
	cloudflared := find(tunnel, "NetworkPolicy", "shelf-cloudflared")
	if cloudflared == nil {
		t.Fatal("an app with a tunnel has no policy that lets cloudflared reach Traefik")
	}
	if sel, _, _ := unstructured.NestedStringMap(cloudflared.Object, "spec", "podSelector", "matchLabels"); sel["app.kubernetes.io/name"] != "cloudflared" {
		t.Errorf("the cloudflared policy selects %v", sel)
	}

	for name, objs := range map[string][]*unstructured.Unstructured{"app": plain, "preview": preview} {
		var ns *unstructured.Unstructured
		for _, o := range objs {
			if o.GetKind() == "Namespace" {
				ns = o
			}
		}
		labels := ns.GetLabels()
		if labels["pod-security.kubernetes.io/enforce"] != "baseline" || labels["pod-security.kubernetes.io/warn"] != "restricted" ||
			labels["pod-security.kubernetes.io/audit"] != "restricted" || labels["pod-security.kubernetes.io/enforce-version"] != "latest" {
			t.Errorf("%s: namespace labels %v", name, labels)
		}
		release := find(objs, "HelmRelease", map[string]string{"app": "hello", "preview": "hello--feature-login"}[name])
		test, _, _ := unstructured.NestedMap(release.Object, "spec", "test")
		if test["enable"] != true || test["ignoreFailures"] != (name == "preview") {
			t.Errorf("%s: test %v, want enabled and failures ignored only in a preview", name, test)
		}
	}
}
