package cluster

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	webDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	dbDigest  = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

const verifiedValues = `name: shop-pkg
components:
  web:
    image: ghcr.io/acme/shop/web:sha-42a964e@` + webDigest + `
  db:
    image: postgres:16@` + dbDigest + `
`

func setCondition(t *testing.T, obj *unstructured.Unstructured, kind, status, message string) {
	t.Helper()
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	kept := []any{}
	for _, c := range conditions {
		if c.(map[string]any)["type"] != kind {
			kept = append(kept, c)
		}
	}
	kept = append(kept, map[string]any{"type": kind, "status": status, "message": message})
	if err := unstructured.SetNestedSlice(obj.Object, kept, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
}

// verifiedApp is an app whose signed and scanned deploy artifact has rolled out.
func verifiedApp(t *testing.T, scan string) (*appObjects, *unstructured.Unstructured) {
	t.Helper()
	o := healthy()
	metadata := map[string]any{
		revisionAnnotation: "main@sha1:42a964eb0b4bd7248305d5f22233d6dbabcd56f3",
		sourceAnnotation:   "https://github.com/acme/shop",
	}
	if scan != "" {
		metadata[ScanAnnotation] = scan
	}
	_ = unstructured.SetNestedMap(o.source.Object, map[string]any{"revision": "main@sha256:new", "metadata": metadata}, "status", "artifact")
	setCondition(t, o.source, sourceVerified, "True", "verified signature of revision main@sha256:new")
	setCondition(t, o.chart, sourceVerified, "True", "verified signature of revision 0.8.0@sha256:c")
	_ = unstructured.SetNestedField(o.kustomization.Object, "main@sha256:new", "status", "lastAppliedRevision")
	inline := map[string]any{"name": "shop"}
	_ = unstructured.SetNestedMap(o.release.Object, inline, "spec", "values")
	digest, err := ConfigDigest(verifiedValues, inline)
	if err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedSlice(o.release.Object, []any{map[string]any{"version": int64(2), "configDigest": digest}}, "status", "history")
	values := &unstructured.Unstructured{Object: map[string]any{"data": map[string]any{"app.yaml": verifiedValues}}}
	return o, values
}

const fullScan = `{"scanner":"trivy 0.75.0","scannedAt":"2026-10-28T12:00:00Z","images":{"` +
	webDigest + `":{"critical":0,"high":2},"` + dbDigest + `":{"critical":2,"high":86}}}`

func TestVerification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		scan       string
		change     func(t *testing.T, o *appObjects, values *unstructured.Unstructured)
		wantReason string
	}{
		{name: "verified", scan: fullScan},
		{name: "findings do not matter", scan: strings.ReplaceAll(fullScan, `"critical":0`, `"critical":9`)},
		{
			name: "unsigned", scan: fullScan,
			change: func(t *testing.T, o *appObjects, _ *unstructured.Unstructured) {
				setCondition(t, o.source, sourceVerified, "False", "no matching signatures were found")
			},
			wantReason: "the deploy artifact is not signed by the cluster's signer: no matching signatures were found",
		},
		{
			name: "platform that does not verify", scan: fullScan,
			change: func(t *testing.T, o *appObjects, _ *unstructured.Unstructured) {
				_ = unstructured.SetNestedSlice(o.source.Object, []any{}, "status", "conditions")
			},
			wantReason: "the cluster does not verify the deploy artifact's signature",
		},
		{
			name: "rollout pending", scan: fullScan,
			change: func(t *testing.T, o *appObjects, _ *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(o.kustomization.Object, "main@sha256:old", "status", "lastAppliedRevision")
			},
			wantReason: "the release does not run the verified deploy artifact yet",
		},
		{
			name: "release on older values", scan: fullScan,
			change: func(t *testing.T, o *appObjects, _ *unstructured.Unstructured) {
				_ = unstructured.SetNestedSlice(o.release.Object, []any{map[string]any{"configDigest": "sha256:older"}}, "status", "history")
			},
			wantReason: "the release does not run the verified deploy artifact yet",
		},
		{
			name: "chart unsigned", scan: fullScan,
			change: func(t *testing.T, o *appObjects, _ *unstructured.Unstructured) {
				setCondition(t, o.chart, sourceVerified, "False", "no matching signatures were found")
			},
			wantReason: "the chart is not signed by the cluster's signer",
		},
		{name: "no scan", wantReason: "the deploy artifact has no scan (dev.shelf.scan)"},
		{name: "unreadable scan", scan: "{", wantReason: "cannot be read"},
		{
			name:       "scan misses an image",
			scan:       `{"scanner":"trivy 0.75.0","images":{"` + webDigest + `":{"critical":0,"high":0}}}`,
			wantReason: "the scan does not cover the image of db",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o, values := verifiedApp(t, tt.scan)
			if tt.change != nil {
				tt.change(t, o, values)
			}
			v := verification("shop", o, values)
			if v.Source != "https://github.com/acme/shop" || v.Commit != "42a964eb0b4bd7248305d5f22233d6dbabcd56f3" {
				t.Errorf("source %q, commit %q", v.Source, v.Commit)
			}
			if tt.wantReason == "" {
				if !v.Verified || v.Reason != "" {
					t.Fatalf("not verified: %s", v.Reason)
				}
				if v.Scan == nil || len(v.Scan.Images) != 2 || v.Scan.Images[0].Component != "db" || v.Scan.Images[0].High != 86 ||
					v.Scan.Images[1].Digest != webDigest || v.Scan.Scanner != "trivy 0.75.0" {
					t.Errorf("scan %+v", v.Scan)
				}
				return
			}
			if v.Verified || !strings.Contains(v.Reason, tt.wantReason) {
				t.Fatalf("verified %v, reason %q, want %q", v.Verified, v.Reason, tt.wantReason)
			}
		})
	}
}

// A test's image runs in the app's namespace like a component's, so the scan has to cover it
// too. The values here are what the release would run; the digest stays that of verifiedValues,
// as the rollout is not what is being checked.
func TestVerificationCoversTests(t *testing.T) {
	t.Parallel()
	const testDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	withTest := verifiedValues + "tests:\n  smoke:\n    image: curlimages/curl:8.10.1@" + testDigest + "\n"
	o, _ := verifiedApp(t, fullScan)
	images, err := valueImages(&unstructured.Unstructured{Object: map[string]any{"data": map[string]any{"app.yaml": withTest}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 3 || images[2].Component != "web" || images[1].Component != "smoke" || images[1].Digest != testDigest {
		t.Errorf("images %+v, want db, smoke and web", images)
	}

	// Run the whole check with values that name the test, and a release that holds them.
	inline := map[string]any{"name": "shop"}
	digest, err := ConfigDigest(withTest, inline)
	if err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedSlice(o.release.Object, []any{map[string]any{"version": int64(2), "configDigest": digest}}, "status", "history")
	values := &unstructured.Unstructured{Object: map[string]any{"data": map[string]any{"app.yaml": withTest}}}
	if v := verification("shop", o, values); v.Verified || v.Reason != "the scan does not cover the image of smoke" {
		t.Errorf("verified %v, reason %q", v.Verified, v.Reason)
	}
	covered := strings.Replace(fullScan, `"images":{`, `"images":{"`+testDigest+`":{"critical":0,"high":0},`, 1)
	o, _ = verifiedApp(t, covered)
	_ = unstructured.SetNestedSlice(o.release.Object, []any{map[string]any{"version": int64(2), "configDigest": digest}}, "status", "history")
	if v := verification("shop", o, values); !v.Verified {
		t.Errorf("not verified with the test's image scanned: %s", v.Reason)
	}
}

func refusal(at, kind, name, message string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Event",
		"metadata":       map[string]any{"name": "e", "namespace": "shop"},
		"involvedObject": map[string]any{"kind": kind, "name": name},
		"reason":         "FailedCreate", "message": message, "lastTimestamp": at,
	}}
}

func podCreated(at string) *unstructured.Unstructured {
	p := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "web-1", "namespace": "shop", "creationTimestamp": at},
		"status":   map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "web", "ready": true}}},
	}}
	return p
}

// TestRefusedPods covers pods the API server never created: the pods stage names the workload,
// the refusal and what to do, and forgets a refusal once a pod came after it.
func TestRefusedPods(t *testing.T) {
	t.Parallel()
	denied := `admission webhook "policy.sigstore.dev" denied the request: validation failed: no signatures found`
	quota := `pods "web-1" is forbidden: exceeded quota: shelf-preview, requested: requests.memory=1Gi`
	tests := []struct {
		name      string
		pods      []*unstructured.Unstructured
		refusals  []*unstructured.Unstructured
		wantPhase Phase
		wantHint  string
	}{
		{"unsigned image, no pod", nil, []*unstructured.Unstructured{refusal("2026-10-28T12:00:00Z", "ReplicaSet", "web-6d7b", denied)},
			PhaseFailed, "not signed by the cluster's signer"},
		{"quota of a preview", nil, []*unstructured.Unstructured{refusal("2026-10-28T12:00:00Z", "StatefulSet", "db", quota)},
			PhaseFailed, "quota is used up"},
		{"refusal after the last pod", []*unstructured.Unstructured{podCreated("2026-10-28T11:00:00Z")},
			[]*unstructured.Unstructured{refusal("2026-10-28T12:00:00Z", "ReplicaSet", "web-6d7b", denied)}, PhaseFailed, "not signed"},
		{"pod after the refusal", []*unstructured.Unstructured{podCreated("2026-10-28T12:05:00Z")},
			[]*unstructured.Unstructured{refusal("2026-10-28T12:00:00Z", "ReplicaSet", "web-6d7b", denied)}, PhaseReady, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := podStage(tt.pods, tt.refusals)
			if s.Phase != tt.wantPhase || !strings.Contains(s.Hint, tt.wantHint) {
				t.Fatalf("stage %+v", s)
			}
			if s.Phase == PhaseFailed && (s.Reason != "FailedCreate" || !strings.HasPrefix(s.Object, tt.refusals[0].Object["involvedObject"].(map[string]any)["kind"].(string)+" shop/")) {
				t.Errorf("stage %+v", s)
			}
		})
	}
}

func TestVerificationErrorHint(t *testing.T) {
	t.Parallel()
	o := healthy()
	_ = unstructured.SetNestedSlice(o.source.Object, []any{map[string]any{
		"type": "Ready", "status": "False", "reason": "VerificationError",
		"message": "failed to verify the signature using provider 'cosign': no matching signatures were found",
	}}, "status", "conditions")
	d := diagnose("shop", o)
	if s := d.Stages[0]; s.Phase != PhaseFailed || !strings.Contains(s.Hint, "id-token: write") {
		t.Errorf("deploy artifact stage %+v", s)
	}
}

func TestFailOnUnverified(t *testing.T) {
	t.Parallel()
	o := healthy()
	ready := failOnUnverified(readyCondition, "push again")
	setCondition(t, o.source, "Ready", "False", "failed to verify the signature")
	setCondition(t, o.source, sourceVerified, "False", "no matching signatures were found")
	if _, _, err := ready(o.source); err == nil || !strings.Contains(err.Error(), "no matching signatures were found\npush again") {
		t.Errorf("an unsigned artifact: %v", err)
	}
	setCondition(t, o.source, sourceVerified, "True", "verified signature")
	if _, _, err := ready(o.source); err != nil {
		t.Errorf("a verified artifact that is not ready yet: %v", err)
	}
}
