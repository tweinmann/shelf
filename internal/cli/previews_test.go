package cli

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
)

func TestPreviewsSwitchedOnAndOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t).public()
	h.app = appWithSecrets("greeter")
	h.defineRegistry(t, "ghcr", "o", "ghcr-secret-token")

	stdout := h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--registry", "ghcr", "--previews")
	if last := h.cluster.added[len(h.cluster.added)-1]; last.Previews != 3 {
		t.Errorf("--previews gave %d previews, want 3", last.Previews)
	}
	if !strings.Contains(stdout, "previews: up to 3 branches, inside the cluster only") {
		t.Errorf("stdout lacks the previews line:\n%s", stdout)
	}

	// Another change keeps the previews, and writes them with the new registry connection.
	h.defineRegistry(t, "other", "o", "ghcr-secret-token")
	h.mustRun(t, "app", "credentials", "greeter", "--registry", "other")
	if last := h.cluster.added[len(h.cluster.added)-1]; last.Previews != 3 || last.Registry != "other" {
		t.Errorf("after a registry change: previews %d, registry %q", last.Previews, last.Registry)
	}

	h.mustRun(t, "app", "credentials", "greeter", "--previews=5")
	if last := h.cluster.added[len(h.cluster.added)-1]; last.Previews != 5 {
		t.Errorf("--previews=5 gave %d", last.Previews)
	}

	stdout = h.mustRun(t, "app", "credentials", "greeter", "--no-previews")
	if last := h.cluster.added[len(h.cluster.added)-1]; last.Previews != 0 {
		t.Errorf("--no-previews left %d previews", last.Previews)
	}
	if !strings.Contains(stdout, "previews: none") {
		t.Errorf("stdout lacks the previews line:\n%s", stdout)
	}
}

func TestPreviewsDefaultOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t).public()
	h.app = appWithSecrets("greeter")
	h.mustRun(t, "app", "add", "greeter", greeterArtifact)
	if last := h.cluster.added[0]; last.Previews != 0 {
		t.Errorf("an app without --previews has %d previews", last.Previews)
	}
}

func TestPreviewsRefusals(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		args []string
		old  bool
		want string
	}{
		"zero":               {args: []string{"--previews=0"}, want: "between 1 and 10 previews"},
		"too many":           {args: []string{"--previews=11"}, want: "between 1 and 10 previews"},
		"number without =":   {args: []string{"--previews", "5"}, want: "write --previews=5"},
		"on and off":         {args: []string{"--previews", "--no-previews"}, want: "switched on and off at once"},
		"platform too old":   {args: []string{"--previews"}, old: true, want: "predates previews"},
		"old platform is ok": {args: []string{}, old: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t).public()
			h.app = appWithSecrets("greeter")
			h.cluster.oldPlatform = tt.old
			_, stderr, code := h.run(t, append([]string{"app", "add", "greeter", greeterArtifact}, tt.args...)...)
			if tt.want == "" {
				if code != 0 {
					t.Errorf("exit code %d: %s", code, stderr)
				}
				return
			}
			if code != 1 || !strings.Contains(stderr, tt.want) {
				t.Errorf("exit code %d, stderr %q, want %q", code, stderr, tt.want)
			}
			if len(h.cluster.added) != 0 {
				t.Error("the app was written before the refusal")
			}
		})
	}
}

// previewHarness is hello with previews of two branches, at most two, so the limit is reached.
func previewHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t).public()
	hello := helloState(cluster.PhaseReady)
	hello.PreviewLimit = 2
	hello.Previews = []cluster.Preview{
		{App: "hello", Slug: "feature-login", AppState: cluster.AppState{Name: "hello--feature-login",
			Artifact: cluster.Artifact{URL: "oci://ghcr.io/o/hello", Tag: "branch-feature-login"},
			Revision: "branch-feature-login@sha256:def", Commit: "3f2a9c1e5b7d9f1a3c5e7b9d1f3a5c7e9b1d3f5a",
			Phase: cluster.PhaseReady}},
		{App: "hello", Slug: "fix-typo", AppState: cluster.AppState{Name: "hello--fix-typo",
			Artifact: cluster.Artifact{URL: "oci://ghcr.io/o/hello", Tag: "branch-fix-typo"},
			Phase:    cluster.PhaseWorking, Stage: "the pods", Reason: "starting: web-0"}},
	}
	h.cluster.states = []cluster.AppState{hello}
	h.cluster.configs = map[string]*cluster.AppConfig{"hello": {Artifact: hello.Artifact, Previews: 2}}
	h.cluster.components = []cluster.Component{{Name: "web", Ports: []cluster.Port{{Name: "main", Number: 80}},
		Path: "/", Phase: cluster.PhaseReady}}
	h.cluster.stages = chain(podsReady)
	return h
}

func TestStatusWithPreviews(t *testing.T) {
	t.Parallel()
	h := previewHarness(t)
	stdout := h.mustRun(t, "app", "status", "hello")
	for _, want := range []string{
		"  previews  2 of at most 2, inside the cluster only\n",
		"    hello--feature-login  Ready  branch-feature-login  3f2a9c1e5b7d\n",
		"    hello--fix-typo  Working  branch-fix-typo\n",
		"the preview limit of 2 is reached",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	list := h.mustRun(t, "app", "status")
	if want := "hello: Ready\n  preview feature-login: Ready\n  preview fix-typo: Working\n"; list != want {
		t.Errorf("list:\n%s\nwant:\n%s", list, want)
	}
	goldenJSON(t, "appstatus-previews", runJSON(t, h, 0, "app", "status", "hello"))
}

func TestStatusOfOnePreview(t *testing.T) {
	t.Parallel()
	h := previewHarness(t)
	stdout := h.mustRun(t, "app", "status", "hello", "--preview", "feature/login")
	for _, want := range []string{"hello--feature-login: Ready", "address   hello--feature-login.shelf.internal",
		"artifact  oci://ghcr.io/o/hello:branch-feature-login", "commit    3f2a9c1e5b7d",
		"verified  yes", "exposure  never: a preview stays inside the cluster"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "previews") {
		t.Errorf("a preview lists previews:\n%s", stdout)
	}
	goldenJSON(t, "appstatus-preview", runJSON(t, h, 0, "app", "status", "hello", "--preview", "feature-login"))

	_, stderr, code := h.run(t, "app", "status", "hello", "--preview", "gone")
	if code != 1 || !strings.Contains(stderr, "no preview of branch gone; it has previews of feature-login, fix-typo") {
		t.Errorf("unknown branch: exit code %d, stderr %q", code, stderr)
	}
}

func TestWaitForAPreview(t *testing.T) {
	t.Parallel()
	h := previewHarness(t)
	// The preview does not exist for the first read, as before the platform saw the branch.
	h.cluster.rollouts, h.cluster.rolloutMissing = []cluster.Rollout{rolledOut(podsReady)}, 1
	out := runJSON(t, h, 0, "app", "status", "hello", "--preview", "feature/login", "--wait")
	if !strings.Contains(string(out), `"app": "hello--feature-login"`) || !strings.Contains(string(out), `"outcome": "Ready"`) {
		t.Errorf("verdict %s", out)
	}

	h = previewHarness(t)
	h.cluster.rolloutMissing = 1000
	stdout, _, code := h.run(t, "app", "status", "hello", "--preview", "new-branch", "--wait", "--timeout", "20ms")
	if code != 2 || !strings.Contains(stdout, "the preview: waiting for the preview of branch new-branch") {
		t.Errorf("a preview that never appears: exit code %d, stdout %q", code, stdout)
	}

	h = previewHarness(t)
	h.cluster.configs["hello"].Previews = 0
	if _, stderr, code := h.run(t, "app", "status", "hello", "--preview", "x", "--wait"); code != 1 ||
		!strings.Contains(stderr, "has no previews; `shelf app credentials hello --previews` switches them on") {
		t.Errorf("previews off: exit code %d, stderr %q", code, stderr)
	}
}

func TestOpenAPreview(t *testing.T) {
	t.Parallel()
	h := previewHarness(t)
	traefik := openHarness(t).cluster.traefik
	h.cluster.traefik = traefik
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var body string
	h.browser = func(url string) error {
		defer cancel()
		resp, err := http.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body = string(b)
		return nil
	}
	stdout, stderr, code := h.runContext(t, ctx, "", "app", "open", "hello", "--preview", "feature/login", "--browser")
	if code != 0 || !strings.Contains(stdout, "hello--feature-login (hello--feature-login.shelf.internal) is at http://127.0.0.1:") {
		t.Fatalf("exit code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if body != "host=hello--feature-login.shelf.internal path=/" {
		t.Errorf("Traefik got %q", body)
	}
	if _, stderr, code := previewHarness(t).run(t, "app", "open", "hello", "--preview", "gone"); code != 1 ||
		!strings.Contains(stderr, "no preview of branch gone") {
		t.Errorf("unknown branch: exit code %d, stderr %q", code, stderr)
	}
}
