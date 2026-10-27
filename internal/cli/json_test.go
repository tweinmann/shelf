package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/testutil"
)

const testCommit = "42a964eb0b4bd7248305d5f22233d6dbabcd56f3"

// fiveStages is the chain of a healthy app; failing replaces the pods stage.
func fiveStages(pods cluster.Stage) []cluster.Stage {
	return []cluster.Stage{
		{Name: "the deploy artifact", Object: "OCIRepository hello/deploy", Phase: cluster.PhaseReady,
			Reason: "Succeeded", Message: "stored artifact for digest 'main@sha256:abc'"},
		{Name: "the app values", Object: "Kustomization hello/deploy", Phase: cluster.PhaseReady,
			Reason: "ReconciliationSucceeded", Message: "Applied revision: main@sha256:abc"},
		{Name: "the chart", Object: "OCIRepository hello/shelf-app", Phase: cluster.PhaseReady,
			Reason: "Succeeded", Message: "stored artifact for digest '0.6.0@sha256:def'"},
		{Name: "the app", Object: "HelmRelease hello/hello", Phase: cluster.PhaseReady,
			Reason: "UpgradeSucceeded", Message: "Helm upgrade succeeded"},
		pods,
	}
}

var (
	podsReady  = cluster.Stage{Name: "the pods", Object: "Pods", Phase: cluster.PhaseReady, Message: "2 running"}
	podsFailed = cluster.Stage{Name: "the pods", Object: "Pod hello/web-6d4f", Phase: cluster.PhaseFailed,
		Reason: "ImagePullBackOff", Message: "Back-off pulling image",
		Hint: "the image cannot be pulled; check the reference and whether the cluster may read that registry"}
)

func helloState(phase cluster.Phase) cluster.AppState {
	return cluster.AppState{
		Name: "hello", Artifact: cluster.Artifact{URL: "oci://ghcr.io/o/hello", Tag: "main"},
		Registry: "ghcr", Revision: "main@sha256:abc", Commit: testCommit,
		Deployed: time.Date(2026, 10, 27, 14, 3, 0, 0, time.FixedZone("CET", 3600)),
		Phase:    phase,
	}
}

// runJSON runs a command with --json and checks what every document has to be: one object on
// stdout, with the envelope, and no null anywhere.
func runJSON(t *testing.T, h *harness, wantCode int, args ...string) []byte {
	t.Helper()
	stdout, stderr, code := h.run(t, append(args, "--json")...)
	if code != wantCode {
		t.Fatalf("%v: exit code %d, want %d: %s", args, code, wantCode, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("%v: stdout is not one JSON object: %v\n%s", args, err, stdout)
	}
	if doc["apiVersion"] != documentAPIVersion || doc["kind"] == "" {
		t.Errorf("%v: envelope %v %v", args, doc["apiVersion"], doc["kind"])
	}
	if strings.Contains(stdout, "null") {
		t.Errorf("%v: a field is null:\n%s", args, stdout)
	}
	for _, token := range []string{"cf-secret-token", "ghcr-secret-token"} {
		if strings.Contains(stdout+stderr, token) {
			t.Fatalf("%v: a token appears in the output", args)
		}
	}
	return []byte(stdout)
}

func goldenJSON(t *testing.T, name string, got []byte) {
	t.Helper()
	testutil.Golden(t, filepath.Join("testdata", "json", name+".json"), got)
}

func TestJSONAppList(t *testing.T) {
	t.Parallel()
	h := newHarness(t).public()
	goldenJSON(t, "applist-empty", runJSON(t, h, 0, "app", "status"))

	failing := helloState(cluster.PhaseFailed)
	failing.Name, failing.Commit, failing.Deployed = "greeter", "", time.Time{}
	failing.Stage, failing.Reason = "the pods", "Back-off pulling image"
	h.cluster.states = []cluster.AppState{failing, helloState(cluster.PhaseReady)}
	goldenJSON(t, "applist", runJSON(t, h, 0, "app", "status"))
}

func TestJSONAppStatus(t *testing.T) {
	t.Parallel()
	components := []cluster.Component{
		{Name: "db", Ports: []cluster.Port{{Name: "main", Number: 5432}}, Phase: cluster.PhaseReady},
		{Name: "web", Ports: []cluster.Port{{Name: "main", Number: 80}}, Path: "/", Phase: cluster.PhaseReady},
	}
	tests := map[string]struct {
		phase cluster.Phase
		pods  cluster.Stage
	}{
		"appstatus":         {cluster.PhaseReady, podsReady},
		"appstatus-failing": {cluster.PhaseFailed, podsFailed},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t).public()
			h.cluster.states = []cluster.AppState{helloState(tt.phase)}
			h.cluster.components, h.cluster.stages = components, fiveStages(tt.pods)
			goldenJSON(t, name, runJSON(t, h, 0, "app", "status", "hello"))
		})
	}
}

func TestJSONError(t *testing.T) {
	t.Parallel()
	h := newHarness(t).public()
	stdout, stderr, code := h.run(t, "app", "status", "nope", "--json")
	want := `{"apiVersion":"shelf.dev/v1alpha1","kind":"Error","message":"app nope does not exist"}`
	var got, wantDoc any
	_ = json.Unmarshal([]byte(want), &wantDoc)
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || !jsonEqual(got, wantDoc) {
		t.Errorf("stdout %s, want %s", stdout, want)
	}
	if code != 1 || !strings.Contains(stderr, "app nope does not exist") {
		t.Errorf("exit code %d, stderr %q", code, stderr)
	}

	// Without --json, an error stays text on stderr only.
	stdout, _, _ = h.run(t, "app", "status", "nope")
	if stdout != "" {
		t.Errorf("text mode printed %q on stdout", stdout)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestJSONTagList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.app = appWithSecrets("greeter")
	h.defineRegistry(t, "ghcr", "o", "ghcr-secret-token")
	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--registry", "ghcr")
	goldenJSON(t, "taglist", runJSON(t, h, 0, "app", "tags", "greeter"))
}

func TestJSONConnectionList(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	goldenJSON(t, "connectionlist-unused", runJSON(t, h, 0, "connection", "list"))

	h.defineRegistry(t, "ghcr", "o", "ghcr-secret-token")
	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--registry", "ghcr",
		"--cloudflare", "home", "--domain", "example.com")
	goldenJSON(t, "connectionlist", runJSON(t, h, 0, "connection", "list"))
}

func TestJSONVersion(t *testing.T) {
	t.Parallel()
	// version takes no cluster flags, so it runs without the harness's --kubeconfig.
	cmd := New(newHarness(t).options())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version", "--json"})
	if code := Execute(context.Background(), cmd); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	goldenJSON(t, "version", out.Bytes())
}

// TestNoJSONForSecrets keeps secret values out of anything an agent would parse and keep.
func TestNoJSONForSecrets(t *testing.T) {
	t.Parallel()
	_, stderr, code := newHarness(t).run(t, "app", "secrets", "hello", "--json")
	if code == 0 || !strings.Contains(stderr, "unknown flag: --json") {
		t.Errorf("exit code %d, stderr %q", code, stderr)
	}
}
