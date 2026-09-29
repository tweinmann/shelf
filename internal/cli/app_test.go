package cli

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/schema"
)

const helloArtifact = "oci://shelf-registry:5000/hello-deploy:main"

func TestAppAdd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.app = appWithSecrets("hello", "db-password")

	stdout, stderr, code := h.run(t, "app", "add", "hello", helloArtifact, "--insecure-registry")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if h.fetched != "shelf-registry:5000/hello-deploy:main" || !h.insecure {
		t.Errorf("fetched %s insecure=%v", h.fetched, h.insecure)
	}
	if h.pull != nil {
		t.Error("with no login to offer, the fetch is left to the Docker config")
	}
	if len(h.cluster.added) != 1 {
		t.Fatalf("add called %d times", len(h.cluster.added))
	}
	added := h.cluster.added[0]
	if added.Name != "hello" || added.Artifact.String() != helloArtifact || !added.Insecure || added.Timeout <= 0 {
		t.Errorf("options %+v", added)
	}
	password := added.Secrets["db-password"]
	if len(password) < 26 {
		t.Fatalf("generated secret %q", password)
	}
	for _, want := range []string{"context   dev", "secret db-password: generated", "secret backup: " + h.backup.Path("hello")} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout+stderr, password) {
		t.Error("the secret value appears in the output")
	}
	saved, err := h.backup.Load("hello")
	if err != nil || saved["db-password"] != password {
		t.Fatalf("backup %v, %v", saved, err)
	}

	// A second add with a new secret keeps the old value and generates the new one.
	h.app = appWithSecrets("hello", "db-password", "api-key")
	stdout, stderr, code = h.run(t, "app", "add", "hello", helloArtifact)
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	second := h.cluster.added[1].Secrets
	if second["db-password"] != password || len(second["api-key"]) < 26 {
		t.Errorf("secrets %v", second)
	}
	if !strings.Contains(stdout, "secret db-password: kept") || !strings.Contains(stdout, "secret api-key: generated") {
		t.Errorf("stdout:\n%s", stdout)
	}

	// After a cluster rebuild the backup restores the values.
	h.cluster.stored = nil
	stdout, _, code = h.run(t, "app", "add", "hello", helloArtifact)
	if code != 0 || h.cluster.added[2].Secrets["db-password"] != password {
		t.Fatalf("code %d, secrets %v", code, h.cluster.added[2].Secrets)
	}
	if !strings.Contains(stdout, "secret db-password: restored from the backup") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestAppAddWithoutSecrets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.app = appWithSecrets("web")

	_, stderr, code := h.run(t, "app", "add", "web", "oci://ghcr.io/o/web-deploy:main")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if len(h.cluster.added[0].Secrets) != 0 {
		t.Errorf("secrets %v", h.cluster.added[0].Secrets)
	}
	if _, err := os.Stat(h.backup.Path("web")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an app without secrets needs no backup: %v", err)
	}
}

func TestAppAddErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		app      *schema.App
		fetchErr error
		wantErr  string
	}{
		{"invalid name", []string{"Hello", helloArtifact}, appWithSecrets("hello"), nil, "DNS label"},
		{"invalid reference", []string{"hello", "shelf-registry:5000/hello-deploy:main"}, appWithSecrets("hello"), nil, "oci://"},
		{"other app", []string{"hello", helloArtifact}, appWithSecrets("other"), nil, `deploys app "other"`},
		{"fetch fails", []string{"hello", helloArtifact}, nil, errors.New("manifest unknown"), "manifest unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.app, h.fetchErr = tt.app, tt.fetchErr
			_, stderr, code := h.run(t, append([]string{"app", "add"}, tt.args...)...)
			if code == 0 || !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("code %d, stderr %q, want %q", code, stderr, tt.wantErr)
			}
			if len(h.cluster.added) != 0 {
				t.Error("nothing may be added")
			}
		})
	}
}

func TestAppRm(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		stdin       string
		found       bool
		wantCode    int
		wantRemoved int
		wantOut     string
		wantErr     string
	}{
		{name: "confirmed", args: []string{"hello"}, stdin: "y\n", found: true, wantRemoved: 1,
			wantOut: "The secret backup stays in "},
		{name: "declined", args: []string{"hello"}, stdin: "n\n", found: true, wantCode: 1, wantErr: "aborted"},
		{name: "unknown app", args: []string{"nope", "--yes"}, wantCode: 1, wantRemoved: 1, wantErr: "app nope does not exist"},
		{name: "invalid name", args: []string{"a_b", "--yes"}, wantCode: 1, wantErr: "DNS label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.cluster.found = tt.found
			if err := h.backup.Save("hello", map[string]string{"db-password": "x"}); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := h.runWithInput(t, tt.stdin, append([]string{"app", "rm"}, tt.args...)...)
			if code != tt.wantCode {
				t.Errorf("exit code %d, want %d: %s", code, tt.wantCode, stderr)
			}
			if len(h.cluster.removed) != tt.wantRemoved {
				t.Errorf("remove called %d times, want %d", len(h.cluster.removed), tt.wantRemoved)
			}
			if !strings.Contains(stdout, tt.wantOut) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantOut, stdout)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr lacks %q: %s", tt.wantErr, stderr)
			}
			if tt.wantRemoved == 1 && !strings.Contains(stdout, "including all its volumes") {
				t.Errorf("the warning is missing:\n%s", stdout)
			}
		})
	}
}

// TestAppAddWithoutCloudflare checks that an app without Cloudflare access runs without being
// published, even under a public domain: exposure belongs to the app, not to the cluster.
func TestAppAddWithoutCloudflare(t *testing.T) {
	t.Parallel()
	h := newHarness(t).public()
	h.app = appWithSecrets("greeter")
	h.env[envCloudflareToken] = "cf-secret-token"

	h.mustRun(t, "app", "add", "greeter", greeterArtifact)
	if len(h.api.records)+len(h.api.created) != 0 {
		t.Errorf("records %v, tunnels %v; a token in the shell is not used without --cloudflare",
			h.api.records, h.api.created)
	}
	if added := h.lastAdded(t); added.TunnelID != "" || added.Registry != nil {
		t.Errorf("options %+v", added)
	}
}

// TestAppStatusComponents pins the three cases the app page and the command both have to show: a
// component a browser reaches, one that answers inside the cluster only, and one with no port at
// all.
func TestAppStatusComponents(t *testing.T) {
	t.Parallel()
	ready := cluster.AppState{Name: "hello", Phase: cluster.PhaseReady}
	exposed := ready
	exposed.Tunnel = "t-1"
	components := []cluster.Component{
		{Name: "check", Phase: cluster.PhaseWorking},
		{Name: "db", Phase: cluster.PhaseReady, Ports: []cluster.Port{{Name: "main", Number: 5432}}},
		{Name: "web", Phase: cluster.PhaseReady, Path: "/",
			Ports: []cluster.Port{{Name: "main", Number: 80}}},
	}
	tests := map[string]struct {
		exposed bool
		local   bool
		own     bool
		want    []string
	}{
		"exposed": {exposed: true, want: []string{
			"    check  Working  inside the cluster only",
			"    db     Ready    db:5432 (inside the cluster only)",
			"    web    Ready    https://hello-dev.example.com/",
		}},
		"a public domain without a tunnel": {want: []string{
			"    web    Ready    hello-dev.example.com/ (not exposed)",
		}},
		"a reserved domain": {local: true, exposed: true, want: []string{
			"    web    Ready    hello-dev.dev.local/ (inside the cluster only)",
		}},
		"a domain of its own": {local: true, own: true, exposed: true, want: []string{
			"  exposed   through tunnel t-1",
			"    web    Ready    https://hello-dev.shop.ch/",
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t).public()
			if tt.local {
				h.cluster.settings.Domain = "dev.local"
			}
			state := ready
			if tt.exposed {
				state = exposed
			}
			if tt.own {
				state.Domain = "shop.ch"
			}
			h.cluster.states, h.cluster.components = []cluster.AppState{state}, components

			stdout, stderr, code := h.run(t, "app", "status", "hello")
			if code != 0 {
				t.Fatalf("exit code %d: %s", code, stderr)
			}
			for _, want := range tt.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			if tt.local && strings.Contains(stdout, "https://hello-dev.dev.local") {
				t.Errorf("a link to a name that cannot exist:\n%s", stdout)
			}
		})
	}
}
