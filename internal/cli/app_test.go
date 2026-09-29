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

// app.yaml's name only names the packages, so one artifact can be added as several apps, each
// with secret values and a backup of its own, and under a name other than its package's.
func TestAppAddOneArtifactTwice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.app = appWithSecrets("greeter", "db-password")

	h.mustRun(t, "app", "add", "hello", helloArtifact)
	h.mustRun(t, "app", "add", "hello-copy", helloArtifact)
	if len(h.cluster.added) != 2 || h.cluster.added[0].Name != "hello" || h.cluster.added[1].Name != "hello-copy" {
		t.Fatalf("added %+v", h.cluster.added)
	}
	first, second := h.cluster.added[0].Secrets["db-password"], h.cluster.added[1].Secrets["db-password"]
	if first == "" || first == second {
		t.Errorf("the two apps share a secret value: %q and %q", first, second)
	}
	for app, want := range map[string]string{"hello": first, "hello-copy": second} {
		saved, err := h.backup.Load(app)
		if err != nil || saved["db-password"] != want {
			t.Errorf("backup of %s: %v, %v", app, saved, err)
		}
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
		{"reserved name", []string{"shelf-system", helloArtifact}, appWithSecrets("hello"), nil, "reserved for the platform"},
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

// TestAppAddWithoutCloudflare checks that an app without a Cloudflare connection runs without
// being published, even with a connection at hand: exposure is chosen per app, not given to the
// cluster.
func TestAppAddWithoutCloudflare(t *testing.T) {
	t.Parallel()
	h := newHarness(t).public()
	h.app = appWithSecrets("greeter")
	h.defineCloudflare(t, "tobile", "cf-secret-token", "")

	h.mustRun(t, "app", "add", "greeter", greeterArtifact)
	if len(h.api.records)+len(h.api.created) != 0 {
		t.Errorf("records %v, tunnels %v; a connection is only used when it is chosen",
			h.api.records, h.api.created)
	}
	if added := h.lastAdded(t); added.TunnelID != "" || added.Registry != "" {
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
	exposed.Tunnel, exposed.Cloudflare = "t-1", "tobile"
	components := []cluster.Component{
		{Name: "check", Phase: cluster.PhaseWorking},
		{Name: "db", Phase: cluster.PhaseReady, Ports: []cluster.Port{{Name: "main", Number: 5432}}},
		{Name: "web", Phase: cluster.PhaseReady, Path: "/",
			Ports: []cluster.Port{{Name: "main", Number: 80}}},
	}
	tests := map[string]struct {
		exposed bool
		domain  string
		want    []string
	}{
		"exposed": {exposed: true, domain: "example.com", want: []string{
			"    check  Working  inside the cluster only",
			"    db     Ready    db:5432 (inside the cluster only)",
			"    web    Ready    https://hello-dev.example.com/",
		}},
		"a public domain without a tunnel": {domain: "example.com", want: []string{
			"    web    Ready    hello-dev.example.com/ (not exposed)",
		}},
		"a reserved domain": {domain: "dev.local", exposed: true, want: []string{
			"    web    Ready    hello-dev.dev.local/ (inside the cluster only)",
		}},
		"no domain": {want: []string{
			"  address   hello.shelf.internal",
			"    web    Ready    hello.shelf.internal/ (inside the cluster only)",
		}},
		"a connection this machine does not hold": {domain: "shop.ch", exposed: true, want: []string{
			"  exposed   through connection tobile, which this machine does not hold",
			"    web    Ready    https://hello-dev.shop.ch/",
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t).public()
			state := ready
			if tt.exposed {
				state = exposed
			}
			state.Domain = tt.domain
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
			if strings.Contains(stdout, "https://hello-dev.dev.local") || strings.Contains(stdout, "https://hello.shelf.internal") {
				t.Errorf("a link to a name that cannot exist:\n%s", stdout)
			}
		})
	}
}
