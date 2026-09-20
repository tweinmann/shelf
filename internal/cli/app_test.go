package cli

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
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

func TestAppAddAndRmPublishTheHostName(t *testing.T) {
	t.Parallel()
	h := newHarness(t).exposed()
	h.app = appWithSecrets("greeter")
	h.cluster.found = true
	h.env[ops.EnvCloudflareToken] = "cf-secret-token"

	stdout, stderr, code := h.run(t, "app", "add", "greeter", "oci://ghcr.io/o/greeter:main")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	want := "greeter-dev.example.com -> t-1.cfargotunnel.com in zone-1"
	if len(h.api.records) != 1 || h.api.records[0] != want {
		t.Errorf("records %v, want %q", h.api.records, want)
	}
	if !strings.Contains(stdout, "DNS greeter-dev.example.com points at t-1.cfargotunnel.com: created") {
		t.Errorf("stdout lacks the record:\n%s", stdout)
	}

	if _, stderr, code = h.run(t, "app", "rm", "greeter", "--yes"); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if len(h.api.deletedRecords) != 1 || h.api.deletedRecords[0] != "greeter-dev.example.com" {
		t.Errorf("deleted %v", h.api.deletedRecords)
	}
	if !strings.Contains(stdout, "DNS greeter-dev.example.com") {
		t.Errorf("stdout lacks the record:\n%s", stdout)
	}
}

func TestAppAddWithoutExposure(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		exposed bool
		token   string
		wantOut string
	}{
		"cluster not exposed": {token: "cf-secret-token"},
		"no token":            {exposed: true, wantOut: "DNS: skipped"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			if tt.exposed {
				h.exposed()
			}
			h.app = appWithSecrets("greeter")
			h.env[ops.EnvCloudflareToken] = tt.token

			stdout, stderr, code := h.run(t, "app", "add", "greeter", "oci://ghcr.io/o/greeter:main")
			if code != 0 {
				t.Fatalf("exit code %d: %s", code, stderr)
			}
			if len(h.api.records) != 0 {
				t.Errorf("no record may be written: %v", h.api.records)
			}
			if tt.wantOut != "" && !strings.Contains(stdout, tt.wantOut) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantOut, stdout)
			}
		})
	}
}

// TestAppStatusComponents pins the three cases the app page and the command both have to show: a
// component a browser reaches, one that answers inside the cluster only, and one with no port at
// all.
func TestAppStatusComponents(t *testing.T) {
	t.Parallel()
	states := []cluster.AppState{{Name: "hello", Phase: cluster.PhaseReady}}
	components := []cluster.Component{
		{Name: "check", Phase: cluster.PhaseWorking},
		{Name: "db", Phase: cluster.PhaseReady, Ports: []cluster.Port{{Name: "main", Number: 5432}}},
		{Name: "web", Phase: cluster.PhaseReady, Path: "/",
			Ports: []cluster.Port{{Name: "main", Number: 80}}},
	}
	tests := map[string]struct {
		exposed bool
		local   bool
		want    []string
	}{
		"exposed": {exposed: true, want: []string{
			"    check  Working  inside the cluster only",
			"    db     Ready    db:5432 (inside the cluster only)",
			"    web    Ready    https://hello-dev.example.com/",
		}},
		"a public domain without a tunnel": {want: []string{
			"    web    Ready    hello.example.com/ (not exposed)",
		}},
		"a reserved domain": {local: true, want: []string{
			"    web    Ready    hello.dev.local/ (inside the cluster only)",
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			switch {
			case tt.exposed:
				h.exposed()
			case tt.local:
				h.cluster.settings = cluster.Settings{Domain: "dev.local", TunnelTarget: "t-1.cfargotunnel.com"}
			default:
				h.cluster.settings = cluster.Settings{Domain: "example.com"}
			}
			h.cluster.states, h.cluster.components = states, components

			stdout, stderr, code := h.run(t, "app", "status", "hello")
			if code != 0 {
				t.Fatalf("exit code %d: %s", code, stderr)
			}
			for _, want := range tt.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			if tt.local && strings.Contains(stdout, "https://hello.dev.local") {
				t.Errorf("a link to a name that cannot exist:\n%s", stdout)
			}
		})
	}
}

// TestAppAddUsesTheClusterLogin covers where the credential for reading a deploy artifact comes
// from. The cluster already holds one, so a machine that registers an app needs no Docker config
// of its own — which is the whole point on a mini, where there is none.
func TestAppAddUsesTheClusterLogin(t *testing.T) {
	t.Parallel()
	login := &cluster.RegistryAuth{Username: "tobi", Token: "secret-token"}
	tests := map[string]struct {
		artifact string
		login    *cluster.RegistryAuth
		wantUser string
	}{
		"the registry the login is for": {
			artifact: "oci://ghcr.io/tweinmann/hello:main", login: login, wantUser: "tobi",
		},
		"another registry gets nothing": {
			artifact: helloArtifact, login: login,
		},
		"no login in the cluster": {
			artifact: "oci://ghcr.io/tweinmann/hello:main",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.app = appWithSecrets("hello")
			h.cluster.login = tt.login

			stdout, stderr, code := h.run(t, "app", "add", "hello", tt.artifact, "--insecure-registry")
			if code != 0 {
				t.Fatalf("exit code %d: %s", code, stderr)
			}
			if strings.Contains(stdout+stderr, "secret-token") {
				t.Fatal("the token appears in the output")
			}
			if tt.wantUser == "" {
				if h.pull != nil {
					t.Errorf("a credential was offered to %s", tt.artifact)
				}
				return
			}
			if h.pull == nil {
				t.Fatal("the cluster's login was not used")
			}
			cfg, err := h.pull.Authorization()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Username != tt.wantUser || cfg.Password != login.Token {
				t.Errorf("offered %s, want %s with its token", cfg.Username, tt.wantUser)
			}
		})
	}
}
