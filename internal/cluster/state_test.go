package cluster

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// fluxObject is a Flux object with one Ready condition, which is all diagnose reads.
func fluxObject(status, reason, message string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"conditions": []any{map[string]any{
				"type": "Ready", "status": status, "reason": reason, "message": message,
			}},
		},
	}}
}

func stalled(message string) *unstructured.Unstructured {
	obj := fluxObject("False", "Progressing", "reconciling")
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	conditions = append(conditions, map[string]any{
		"type": "Stalled", "status": "True", "reason": "RetriesExceeded", "message": message,
	})
	_ = unstructured.SetNestedSlice(obj.Object, conditions, "status", "conditions")
	return obj
}

func pod(phase string, containers ...map[string]any) *unstructured.Unstructured {
	statuses := make([]any, 0, len(containers))
	for _, c := range containers {
		statuses = append(statuses, c)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "web-0", "namespace": "shop"},
		"status":   map[string]any{"phase": phase, "containerStatuses": statuses},
	}}
}

func container(ready bool, state map[string]any) map[string]any {
	c := map[string]any{"name": "web", "ready": ready}
	if state != nil {
		c["state"] = state
	}
	return c
}

func waiting(reason, message string) map[string]any {
	return map[string]any{"waiting": map[string]any{"reason": reason, "message": message}}
}

// healthy is an app where every Flux object is ready.
func healthy() *appObjects {
	return &appObjects{
		provider:      &unstructured.Unstructured{Object: map[string]any{}},
		source:        fluxObject("True", "Succeeded", "stored artifact"),
		kustomization: fluxObject("True", "ReconciliationSucceeded", "Applied revision: main@sha256:1"),
		chart:         fluxObject("True", "Succeeded", "stored artifact"),
		release:       fluxObject("True", "InstallSucceeded", "Helm install succeeded"),
		pods:          []*unstructured.Unstructured{pod("Running", container(true, nil))},
	}
}

func TestDiagnose(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		objects     func(*appObjects)
		wantPhase   Phase
		wantStage   string
		wantMessage string
		wantHint    string
	}{
		{
			name:      "healthy",
			objects:   func(*appObjects) {},
			wantPhase: PhaseReady,
		},
		{
			name:        "the artifact cannot be pulled",
			objects:     func(o *appObjects) { o.source = fluxObject("False", "OCIArtifactPullFailed", "MANIFEST_UNKNOWN") },
			wantPhase:   PhaseFailed,
			wantStage:   "the deploy artifact",
			wantMessage: "MANIFEST_UNKNOWN",
			wantHint:    "shelf cannot read the deploy artifact from the registry",
		},
		{
			name:        "the registry refuses the login",
			objects:     func(o *appObjects) { o.source = fluxObject("False", "OCIArtifactPullFailed", "unauthorized: denied") },
			wantPhase:   PhaseFailed,
			wantStage:   "the deploy artifact",
			wantMessage: "unauthorized: denied",
			wantHint: "the registry refused to hand out the deploy artifact; give the app a registry connection that may read it " +
				"(`shelf app credentials shop --registry <connection>`), or a new token to the one it has " +
				"(`shelf connection add registry <connection>`)",
		},
		{
			name:      "the controller is still working",
			objects:   func(o *appObjects) { o.source = fluxObject("False", "Progressing", "pulling artifact") },
			wantPhase: PhaseWorking,
			wantStage: "the deploy artifact",
		},
		{
			name:      "the platform has not created the app",
			objects:   func(o *appObjects) { o.source = nil },
			wantPhase: PhaseMissing,
			wantStage: "the deploy artifact",
		},
		{
			name:        "the artifact was rejected",
			objects:     func(o *appObjects) { o.kustomization = fluxObject("False", "ReconciliationFailed", "forbidden") },
			wantPhase:   PhaseFailed,
			wantStage:   "the app values",
			wantMessage: "forbidden",
			wantHint:    "the deploy artifact was rejected; it may hold objects an app may not create",
		},
		{
			name:        "the release gave up",
			objects:     func(o *appObjects) { o.release = stalled("upgrade retries exhausted") },
			wantPhase:   PhaseFailed,
			wantStage:   "the app",
			wantMessage: "upgrade retries exhausted",
			wantHint:    "the app.yaml was rendered but the release did not succeed",
		},
		{
			name: "the image does not exist",
			objects: func(o *appObjects) {
				o.pods = []*unstructured.Unstructured{
					pod("Pending", container(false, waiting("ImagePullBackOff", `Back-off pulling image "nope"`))),
				}
			},
			wantPhase:   PhaseFailed,
			wantStage:   "the pods",
			wantMessage: `Back-off pulling image "nope"`,
			wantHint:    "the image cannot be pulled; check the reference and whether the cluster may read that registry",
		},
		{
			// A secret added to app.yaml and rolled out by Flux alone: the chart asks for a
			// value only shelf can generate, and shelf was never asked to.
			name: "a secret the app declares was never generated",
			objects: func(o *appObjects) {
				o.pods = []*unstructured.Unstructured{
					pod("Pending", container(false, waiting("CreateContainerConfigError",
						"couldn't find key db-password in Secret shop/shelf-secrets"))),
				}
			},
			wantPhase:   PhaseFailed,
			wantStage:   "the pods",
			wantMessage: "couldn't find key db-password in Secret shop/shelf-secrets",
			wantHint: "the app declares the secret db-password and shelf has not generated it yet; " +
				"deploy the app again (`shelf app add`, or Deploy on its page) so that it does",
		},
		{
			// Another config problem keeps the general hint; only the app's own Secret means
			// the story above.
			name: "a config map the app asks for is missing",
			objects: func(o *appObjects) {
				o.pods = []*unstructured.Unstructured{
					pod("Pending", container(false, waiting("CreateContainerConfigError",
						"configmap \"nope\" not found"))),
				}
			},
			wantPhase:   PhaseFailed,
			wantStage:   "the pods",
			wantMessage: `configmap "nope" not found`,
			wantHint:    "the container cannot be configured, usually a missing secret or config map",
		},
		{
			name: "a pod is starting",
			objects: func(o *appObjects) {
				o.pods = []*unstructured.Unstructured{
					pod("Pending", container(false, waiting("ContainerCreating", ""))),
				}
			},
			wantPhase: PhaseWorking,
			wantStage: "the pods",
		},
		{
			name:      "no pods yet",
			objects:   func(o *appObjects) { o.pods = nil },
			wantPhase: PhaseMissing,
			wantStage: "the pods",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			objects := healthy()
			tt.objects(objects)
			d := diagnose("shop", objects)

			if len(d.Stages) != 5 {
				t.Fatalf("%d stages, want the whole chain", len(d.Stages))
			}
			if got := d.Phase(); got != tt.wantPhase {
				t.Errorf("phase %s, want %s", got, tt.wantPhase)
			}
			trouble := d.Trouble()
			if tt.wantStage == "" {
				if trouble != nil {
					t.Fatalf("a healthy app reports trouble: %+v", trouble)
				}
				return
			}
			if trouble == nil {
				t.Fatal("no trouble reported")
			}
			if trouble.Name != tt.wantStage {
				t.Errorf("stage %q, want %q", trouble.Name, tt.wantStage)
			}
			if tt.wantMessage != "" && trouble.Message != tt.wantMessage {
				t.Errorf("message %q, want %q", trouble.Message, tt.wantMessage)
			}
			if trouble.Hint != tt.wantHint {
				t.Errorf("hint %q, want %q", trouble.Hint, tt.wantHint)
			}
		})
	}
}

// TestDiagnoseReportsTheFirstProblem checks the rule the page rests on: a later stage that is
// also broken is a consequence, so the first one is what a person has to act on.
func TestDiagnoseReportsTheFirstProblem(t *testing.T) {
	t.Parallel()
	objects := healthy()
	objects.source = fluxObject("False", "OCIArtifactPullFailed", "MANIFEST_UNKNOWN")
	objects.release = fluxObject("False", "UpgradeFailed", "no chart values")
	trouble := diagnose("shop", objects).Trouble()
	if trouble == nil || trouble.Name != "the deploy artifact" {
		t.Fatalf("trouble %+v, want the deploy artifact", trouble)
	}
}

func TestAppState(t *testing.T) {
	t.Parallel()
	objects := healthy()
	objects.provider = &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"defaultValues": map[string]any{
			"name": "shop", "url": "oci://ghcr.io/o/shop", "tag": "main", "insecure": true,
			"domain": "shop.example", "tunnel": "t-1", "registry": "ghcr", "cloudflare": "tobile",
		}},
	}}
	_ = unstructured.SetNestedMap(objects.source.Object,
		map[string]any{"revision": "main@sha256:abc"}, "status", "artifact")

	state := appState("shop", objects)
	if state.Name != "shop" || state.Artifact.String() != "oci://ghcr.io/o/shop:main" || !state.Insecure {
		t.Errorf("state %+v", state)
	}
	if state.Domain != "shop.example" || state.Tunnel != "t-1" || state.Registry != "ghcr" ||
		state.Cloudflare != "tobile" {
		t.Errorf("registration %+v", state)
	}
	if state.Revision != "main@sha256:abc" {
		t.Errorf("revision %q", state.Revision)
	}
	if state.Phase != PhaseReady || state.Reason != "" || state.Stage != "" {
		t.Errorf("a healthy app reports %+v", state)
	}
}

func TestShortenedMessages(t *testing.T) {
	t.Parallel()
	objects := healthy()
	objects.source = fluxObject("False", "OCIArtifactPullFailed", "  MANIFEST_UNKNOWN  ")
	state := appState("shop", objects)
	if state.Reason != "MANIFEST_UNKNOWN" {
		t.Errorf("reason %q; it is shown in a list and has to be one line", state.Reason)
	}
	if state.Stage != "the deploy artifact" {
		t.Errorf("stage %q", state.Stage)
	}
}
