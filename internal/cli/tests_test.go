package cli

import (
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
)

// failedTestHarness is hello with a test that failed after its pods came up, and a database
// that would break Pod Security restricted.
func failedTestHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t).public()
	h.cluster.states = []cluster.AppState{helloState(cluster.PhaseFailed)}
	h.cluster.components = []cluster.Component{
		{Name: "web", Ports: []cluster.Port{{Name: "main", Number: 80}}, Path: "/", Phase: cluster.PhaseReady},
	}
	stages := chain(podsReady)
	stages[5] = cluster.Stage{Name: "the tests", Object: "Pod hello/hello-test-smoke", Phase: cluster.PhaseFailed,
		Reason: "TestFailed", Message: "test smoke failed with exit code 22:\ncurl: (22) The requested URL returned error: 500\nretrying would not help",
		Hint: "the app's own test smoke fails against this version; the previous version runs again. Its log says why"}
	h.cluster.stages = stages
	code := 22
	h.cluster.tests = []cluster.TestResult{{Name: "smoke", Phase: cluster.PhaseFailed, ExitCode: &code,
		Log: "curl: (22) The requested URL returned error: 500\nretrying would not help"}}
	h.cluster.isolations = map[string]cluster.Isolation{"hello": {PodSecurity: "baseline", Restricted: []cluster.Violation{
		{Pod: "db-0", Reason: "allowPrivilegeEscalation != false, runAsNonRoot != true"},
	}}}
	return h
}

func TestStatusOfAFailedTest(t *testing.T) {
	t.Parallel()
	h := failedTestHarness(t)
	stdout := h.mustRun(t, "app", "status", "hello")
	for _, want := range []string{
		"  Failed    the tests\n" +
			"            test smoke failed with exit code 22:\n" +
			"            curl: (22) The requested URL returned error: 500\n" +
			"            retrying would not help\n" +
			"            the app's own test smoke fails against this version; the previous version runs again. Its log says why\n",
		"  pod security  baseline\n    restricted would refuse db-0: allowPrivilegeEscalation != false, runAsNonRoot != true\n",
		"  lan           none\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	goldenJSON(t, "appstatus-test-failed", runJSON(t, h, 0, "app", "status", "hello"))
}

func TestStatusShowsLANExceptions(t *testing.T) {
	t.Parallel()
	h := newHarness(t).public()
	h.app = appWithSecrets("hello")
	h.mustRun(t, "app", "add", "hello", helloArtifact, "--insecure-registry", "--allow-lan", "192.168.1.10/32")
	h.cluster.stages = chain(podsReady)
	if stdout := h.mustRun(t, "app", "status", "hello"); !strings.Contains(stdout, "  lan           192.168.1.10/32\n") {
		t.Errorf("stdout:\n%s", stdout)
	}
}
