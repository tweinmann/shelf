package cluster

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// testedRelease is a HelmRelease with tests switched on, its conditions and its history, newest
// first, as helm-controller reports them (seen on the dev cluster on 2026-10-28).
func testedRelease(ignoreFailures bool, attempted string, conditions []any, history ...any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"test": map[string]any{"enable": true, "ignoreFailures": ignoreFailures}},
		"status": map[string]any{
			"conditions":                conditions,
			"history":                   history,
			"lastAttemptedConfigDigest": attempted,
		},
	}}
}

func cond(kind, status, reason, message string) map[string]any {
	return map[string]any{"type": kind, "status": status, "reason": reason, "message": message}
}

func snapshot(version int64, digest string, hooks map[string]any) map[string]any {
	entry := map[string]any{"version": version, "configDigest": digest}
	if hooks != nil {
		entry["testHooks"] = hooks
	}
	return entry
}

func hook(phase string) map[string]any { return map[string]any{"phase": phase} }

func valuesWithTests(tests ...string) *unstructured.Unstructured {
	app := "components:\n  web: { image: nginx }\n"
	if len(tests) > 0 {
		app += "tests:\n"
		for _, t := range tests {
			app += "  " + t + ": { image: curl }\n"
		}
	}
	return &unstructured.Unstructured{Object: map[string]any{"data": map[string]any{"app.yaml": app}}}
}

func testPod(name string, phase string, exit int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": name, "namespace": "shop", "labels": map[string]any{TestLabel: strings.TrimPrefix(name, "shop-test-")}},
		"status": map[string]any{"phase": phase, "containerStatuses": []any{
			map[string]any{"name": "smoke", "state": map[string]any{"terminated": map[string]any{"exitCode": exit}}},
		}},
	}}
}

func TestTestsStage(t *testing.T) {
	t.Parallel()
	ready := []any{cond("Ready", "True", "TestSucceeded", "Helm test succeeded"), cond("Released", "True", "UpgradeSucceeded", "")}
	// What an app looks like after an upgrade whose test failed: rolled back to v1, whose values
	// came back as v3 without tests, while v2 holds the failed result.
	rolledBack := testedRelease(false, "sha256:2", []any{
		cond("Ready", "False", "RollbackSucceeded", "Helm rollback to previous release shop/shop.v1 succeeded"),
		cond("Released", "True", "UpgradeSucceeded", ""),
		cond("TestSuccess", "False", "TestFailed", "Helm test failed for release shop/shop.v2"),
		cond("Remediated", "True", "RollbackSucceeded", ""),
	}, snapshot(3, "sha256:1", nil), snapshot(2, "sha256:2", map[string]any{"shop-test-smoke": hook("Failed")}),
		snapshot(1, "sha256:1", map[string]any{"shop-test-smoke": hook("Succeeded")}))

	tests := []struct {
		name        string
		objects     func(o *appObjects)
		wantPhase   Phase
		wantStage   string // the first stage that is not Ready, "" for none
		wantTests   string // the stage's message
		wantDigest  string
		wantResults string
	}{
		{
			name:      "a platform from before tests",
			objects:   func(o *appObjects) {},
			wantPhase: PhaseReady, wantTests: "this platform does not run tests",
		},
		{
			name: "an app without tests",
			objects: func(o *appObjects) {
				o.values = valuesWithTests()
				o.release = testedRelease(false, "sha256:1", ready, snapshot(1, "sha256:1", nil))
			},
			wantPhase: PhaseReady, wantTests: "the app declares no tests", wantDigest: "sha256:1",
		},
		{
			name: "the release has not got to the new values",
			objects: func(o *appObjects) {
				o.values = valuesWithTests("smoke")
				o.release = testedRelease(false, "sha256:2", ready, snapshot(1, "sha256:1", map[string]any{"shop-test-smoke": hook("Succeeded")}))
			},
			wantPhase: PhaseWorking, wantStage: "the tests", wantTests: "waiting for the release",
		},
		{
			name: "a test is running",
			objects: func(o *appObjects) {
				o.values = valuesWithTests("smoke", "login")
				o.release = testedRelease(false, "sha256:2", []any{cond("Ready", "Unknown", "AwaitingTests", "")},
					snapshot(2, "sha256:2", map[string]any{"shop-test-login": hook("Succeeded"), "shop-test-smoke": hook("Running")}))
				o.tests = []*unstructured.Unstructured{testPod("shop-test-smoke", "Running", 0)}
			},
			wantPhase: PhaseWorking, wantStage: "the app", wantTests: "running: smoke", wantDigest: "sha256:2",
			wantResults: "login=Ready smoke=Working",
		},
		{
			name: "all tests passed",
			objects: func(o *appObjects) {
				o.values = valuesWithTests("smoke", "login")
				o.release = testedRelease(false, "sha256:2", ready,
					snapshot(2, "sha256:2", map[string]any{"shop-test-login": hook("Succeeded"), "shop-test-smoke": hook("Succeeded")}))
				o.tests = []*unstructured.Unstructured{testPod("shop-test-smoke", "Succeeded", 0), testPod("shop-test-login", "Succeeded", 0)}
			},
			wantPhase: PhaseReady, wantTests: "2 passed: login, smoke", wantDigest: "sha256:2",
			wantResults: "login=Ready smoke=Ready",
		},
		{
			name: "a failed test rolled the app back",
			objects: func(o *appObjects) {
				o.values = valuesWithTests("smoke")
				o.release = rolledBack
				o.tests = []*unstructured.Unstructured{testPod("shop-test-smoke", "Failed", 1)}
			},
			wantPhase: PhaseFailed, wantStage: "the tests", wantTests: "test smoke failed with exit code 1", wantDigest: "sha256:2",
			wantResults: "smoke=Failed",
		},
		{
			name: "a failed test on a preview",
			objects: func(o *appObjects) {
				o.values = valuesWithTests("smoke")
				o.release = testedRelease(true, "sha256:2", []any{
					cond("Ready", "True", "UpgradeSucceeded", ""), cond("Released", "True", "UpgradeSucceeded", ""),
					cond("TestSuccess", "False", "TestFailed", "Helm test failed"),
				}, snapshot(2, "sha256:2", map[string]any{"shop-test-smoke": hook("Failed")}))
				o.tests = []*unstructured.Unstructured{testPod("shop-test-smoke", "Failed", 7)}
			},
			wantPhase: PhaseFailed, wantStage: "the tests", wantTests: "test smoke failed with exit code 7", wantDigest: "sha256:2",
			wantResults: "smoke=Failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			objects := healthy()
			tt.objects(objects)
			d := diagnose("shop", objects)
			if len(d.Stages) != 6 || d.Stages[5].Name != "the tests" {
				t.Fatalf("stages %+v, want the tests last", d.Stages)
			}
			if d.Phase() != tt.wantPhase {
				t.Errorf("phase %s, want %s", d.Phase(), tt.wantPhase)
			}
			if trouble := d.Trouble(); (trouble == nil) != (tt.wantStage == "") || (trouble != nil && trouble.Name != tt.wantStage) {
				t.Errorf("trouble %+v, want stage %q", trouble, tt.wantStage)
			}
			if got := d.Stages[5].Message; got != tt.wantTests {
				t.Errorf("tests stage %q, want %q", got, tt.wantTests)
			}
			if d.testedDigest != tt.wantDigest {
				t.Errorf("tested digest %q, want %q", d.testedDigest, tt.wantDigest)
			}
			var results []string
			for _, r := range d.Tests {
				results = append(results, r.Name+"="+string(r.Phase))
			}
			if got := strings.Join(results, " "); got != tt.wantResults {
				t.Errorf("results %q, want %q", got, tt.wantResults)
			}
		})
	}

	// The release stage of a rolled-back app is not the trouble: the tests stage says what is.
	o := healthy()
	o.values, o.release = valuesWithTests("smoke"), rolledBack
	if s := diagnose("shop", o).Stages[3]; s.Phase != PhaseReady || s.Hint != "" {
		t.Errorf("release stage %+v, want Ready after a rollback caused by a test", s)
	}
	failed := diagnose("shop", func() *appObjects {
		o := healthy()
		o.values, o.release = valuesWithTests("smoke"), rolledBack
		return o
	}()).Stages[5]
	if !strings.Contains(failed.Hint, "the previous version runs again") || failed.Object != "Pod shop/shop-test-smoke" {
		t.Errorf("failed tests stage %+v", failed)
	}
}

// A test pod that ended, whether it passed or failed, is no pod of the app's.
func TestTestPodsAreNoAppPods(t *testing.T) {
	t.Parallel()
	o := healthy()
	o.tests = []*unstructured.Unstructured{testPod("shop-test-smoke", "Failed", 1)}
	if s := diagnose("shop", o).Stages[4]; s.Phase != PhaseReady {
		t.Errorf("pods stage %+v, want Ready", s)
	}
}
