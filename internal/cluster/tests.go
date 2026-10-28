package cluster

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/tweinmann/shelf/internal/render"
)

// TestLabel marks the pods of an app's tests, which the chart renders as Helm test hooks. They
// carry the app's label as well, but are no part of its pods.
const TestLabel = "shelf.dev/test"

// testLogLines is how much of a failed test's log the tests stage shows.
const testLogLines = 20

// TestResult is how one test of an app went for the values the release last tried.
type TestResult struct {
	Name  string `json:"name"`
	Phase Phase  `json:"phase"`
	// ExitCode is the exit status of a test that failed.
	ExitCode *int `json:"exitCode,omitempty"`
	// Log is the end of a failed test's log; only read for one app, never for a list.
	Log string `json:"log,omitempty"`
	// pod is the test's pod, to read the log from.
	pod string
}

// testsStage reports on the tests of the values helm-controller last tried, which is what a new
// commit is waiting for: after a failed upgrade and its rollback, the release runs older values,
// whose tests passed long ago, while the result that matters is that of the attempt.
//
// helm-controller runs the tests after every install and upgrade and records them in the history
// entry of that Helm revision, by pod name. A rollback is a revision of its own without tests,
// but it has the configDigest of the revision it went back to.
func testsStage(name string, o *appObjects) (Stage, []TestResult, string) {
	s := Stage{Name: "the tests", Object: "Pods " + name + "/" + name + "-test-*"}
	if o.release == nil {
		s.Phase, s.Message = PhaseMissing, "the platform has not created the app yet"
		return s, nil, ""
	}
	if enabled, _, _ := unstructured.NestedBool(o.release.Object, "spec", "test", "enable"); !enabled {
		s.Phase, s.Message = PhaseReady, "this platform does not run tests"
		return s, nil, ""
	}
	declared := declaredTests(o.values)
	attempted, _, _ := unstructured.NestedString(o.release.Object, "status", "lastAttemptedConfigDigest")
	if len(declared) == 0 {
		s.Phase, s.Message = PhaseReady, "the app declares no tests"
		return s, nil, attempted
	}
	hooks, found := attemptedHooks(o.release, attempted)
	if !found {
		s.Phase, s.Message = PhaseWorking, "waiting for the release"
		return s, nil, ""
	}

	pods := map[string]*unstructured.Unstructured{}
	for _, p := range o.tests {
		pods[p.GetName()] = p
	}
	prefix := name + "-test-"
	var results []TestResult
	for _, test := range declared {
		r := TestResult{Name: test, Phase: PhaseWorking, pod: prefix + test}
		switch hooks[prefix+test] {
		case "Succeeded":
			r.Phase = PhaseReady
		case "Failed":
			r.Phase = PhaseFailed
			r.ExitCode = exitCode(pods[r.pod])
		}
		results = append(results, r)
	}

	var running, passed []string
	for _, r := range results {
		switch r.Phase {
		case PhaseFailed:
			release := "the previous version runs again"
			if ignore, _, _ := unstructured.NestedBool(o.release.Object, "spec", "test", "ignoreFailures"); ignore {
				release = "the preview keeps running it, so it can be looked at with shelf app open --preview"
			}
			s.Phase, s.Reason, s.Object = PhaseFailed, "TestFailed", "Pod "+name+"/"+r.pod
			s.Message = "test " + r.Name + " failed"
			if r.ExitCode != nil {
				s.Message += fmt.Sprintf(" with exit code %d", *r.ExitCode)
			}
			s.Hint = "the app's own test " + r.Name + " fails against this version; " + release +
				". Its log says why"
			return s, results, attempted
		case PhaseReady:
			passed = append(passed, r.Name)
		default:
			running = append(running, r.Name)
		}
	}
	if len(running) > 0 {
		s.Phase, s.Message = PhaseWorking, "running: "+strings.Join(running, ", ")
		return s, results, attempted
	}
	s.Phase, s.Message = PhaseReady, fmt.Sprintf("%d passed: %s", len(passed), strings.Join(passed, ", "))
	return s, results, attempted
}

// attemptedHooks returns the test hooks helm-controller recorded for the newest Helm revision with
// the given configDigest that ran its tests, and whether there is such a revision yet.
func attemptedHooks(release *unstructured.Unstructured, digest string) (map[string]string, bool) {
	if digest == "" {
		return nil, false
	}
	history, _, _ := unstructured.NestedSlice(release.Object, "status", "history")
	seen := false
	for _, item := range history {
		entry, ok := item.(map[string]any)
		if !ok || entry["configDigest"] != digest {
			continue
		}
		seen = true
		hooks, _ := entry["testHooks"].(map[string]any)
		if len(hooks) == 0 {
			continue
		}
		out := map[string]string{}
		for pod, h := range hooks {
			if hook, ok := h.(map[string]any); ok {
				out[pod], _ = hook["phase"].(string)
			}
		}
		return out, true
	}
	// A revision with these values exists, but its tests have not been recorded yet.
	return map[string]string{}, seen
}

// declaredTests are the names of the tests in the app's values, sorted.
func declaredTests(values *unstructured.Unstructured) []string {
	if values == nil {
		return nil
	}
	data, _, _ := unstructured.NestedString(values.Object, "data", render.ConfigMapKey)
	var app struct {
		Tests map[string]any `json:"tests"`
	}
	if err := yaml.Unmarshal([]byte(data), &app); err != nil {
		return nil
	}
	return slices.Sorted(maps.Keys(app.Tests))
}

// exitCode is the exit status of a test pod's container, nil when it has none.
func exitCode(pod *unstructured.Unstructured) *int {
	if pod == nil {
		return nil
	}
	statuses, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	for _, item := range statuses {
		cs, _ := item.(map[string]any)
		if terminated, ok := nested(cs, "state", "terminated"); ok {
			if code, ok := terminated["exitCode"].(int64); ok {
				n := int(code)
				return &n
			}
		}
	}
	return nil
}

// addTestLogs puts the end of the log of every failed test into its result and into the tests
// stage, which then says why without anyone running kubectl logs.
func (c *client) addTestLogs(ctx context.Context, namespace string, d *Diagnosis) error {
	var clientset *kubernetes.Clientset
	for i := range d.Tests {
		r := &d.Tests[i]
		if r.Phase != PhaseFailed {
			continue
		}
		if clientset == nil {
			var err error
			if clientset, err = kubernetes.NewForConfig(c.cfg); err != nil {
				return err
			}
		}
		lines := int64(testLogLines)
		log, err := clientset.CoreV1().Pods(namespace).GetLogs(r.pod, &corev1.PodLogOptions{TailLines: &lines}).DoRaw(ctx)
		if err != nil {
			// The pod is gone, as after the next run started; the result stands without its log.
			continue
		}
		r.Log = strings.TrimRight(string(log), "\n")
		for j := range d.Stages {
			if s := &d.Stages[j]; s.Name == "the tests" && s.Phase == PhaseFailed && strings.HasSuffix(s.Object, "/"+r.pod) && r.Log != "" {
				s.Message += ":\n" + r.Log
			}
		}
	}
	return nil
}
