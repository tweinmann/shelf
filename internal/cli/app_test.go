package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/schema"
	"github.com/tweinmann/shelf/internal/secrets"
)

// fakeApps replaces registry and cluster access for the app commands.
type fakeApps struct {
	settings cluster.Settings
	api      *fakeCloudflare
	app      *schema.App
	fetchErr error
	stored   map[string]string
	added    []cluster.AppOptions
	removed  []string
	found    bool
	insecure bool
	ref      string
}

func (f *fakeApps) install(t *testing.T) {
	t.Helper()
	oldFetch, oldSecrets, oldAdd, oldRemove := fetchApp, appSecrets, addApp, removeApp
	oldSettings, oldNew := clusterSettings, newCloudflare
	t.Cleanup(func() {
		fetchApp, appSecrets, addApp, removeApp = oldFetch, oldSecrets, oldAdd, oldRemove
		clusterSettings, newCloudflare = oldSettings, oldNew
	})
	clusterSettings = func(context.Context, *rest.Config) (cluster.Settings, error) { return f.settings, nil }
	newCloudflare = func(string) cloudflareAPI { return f.api }
	fetchApp = func(_ context.Context, ref string, insecure bool) (*schema.App, error) {
		f.ref, f.insecure = ref, insecure
		return f.app, f.fetchErr
	}
	appSecrets = func(context.Context, *rest.Config, string) (map[string]string, error) {
		return f.stored, nil
	}
	addApp = func(_ context.Context, _ *rest.Config, o cluster.AppOptions) error {
		f.added = append(f.added, o)
		f.stored = o.Secrets
		return nil
	}
	removeApp = func(_ context.Context, _ *rest.Config, name string, _ time.Duration, _ io.Writer) (bool, error) {
		f.removed = append(f.removed, name)
		return f.found, nil
	}
}

// exposedApps is a cluster whose apps are reachable from the internet.
func exposedApps(f *fakeApps) {
	f.settings = cluster.Settings{Domain: "example.com", HostSuffix: "-dev", TunnelTarget: "t-1.cfargotunnel.com"}
	f.api = &fakeCloudflare{}
}

func appWithSecrets(name string, secretNames ...string) *schema.App {
	app := &schema.App{APIVersion: schema.APIVersion, Name: name, Secrets: map[string]*schema.Secret{}}
	for _, s := range secretNames {
		app.Secrets[s] = &schema.Secret{Generate: true}
	}
	return app
}

func appTestEnv(t *testing.T) (kubeconfig string, backup secrets.Backup) {
	t.Helper()
	kubeconfig = filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kubeconfig, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("SHELF_HOME", home)
	return kubeconfig, secrets.Backup{Dir: filepath.Join(home, "apps")}
}

const helloArtifact = "oci://shelf-registry:5000/hello-deploy:main"

func TestAppAdd(t *testing.T) {
	kubeconfig, backup := appTestEnv(t)
	fake := &fakeApps{app: appWithSecrets("hello", "db-password")}
	fake.install(t)

	stdout, stderr, code := run(t, "app", "add", "hello", helloArtifact, "--insecure-registry", "--kubeconfig", kubeconfig)
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if fake.ref != "shelf-registry:5000/hello-deploy:main" || !fake.insecure {
		t.Errorf("fetched %s insecure=%v", fake.ref, fake.insecure)
	}
	if len(fake.added) != 1 {
		t.Fatalf("add called %d times", len(fake.added))
	}
	added := fake.added[0]
	if added.Name != "hello" || added.Artifact.String() != helloArtifact || !added.Insecure || added.Timeout <= 0 {
		t.Errorf("options %+v", added)
	}
	password := added.Secrets["db-password"]
	if len(password) < 26 {
		t.Fatalf("generated secret %q", password)
	}
	for _, want := range []string{"context   dev", "secret db-password: generated", "secret backup: " + backup.Path("hello")} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout+stderr, password) {
		t.Error("the secret value appears in the output")
	}
	saved, err := backup.Load("hello")
	if err != nil || saved["db-password"] != password {
		t.Fatalf("backup %v, %v", saved, err)
	}

	// A second add with a new secret keeps the old value and generates the new one.
	fake.app = appWithSecrets("hello", "db-password", "api-key")
	stdout, stderr, code = run(t, "app", "add", "hello", helloArtifact, "--kubeconfig", kubeconfig)
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	second := fake.added[1].Secrets
	if second["db-password"] != password || len(second["api-key"]) < 26 {
		t.Errorf("secrets %v", second)
	}
	if !strings.Contains(stdout, "secret db-password: kept") || !strings.Contains(stdout, "secret api-key: generated") {
		t.Errorf("stdout:\n%s", stdout)
	}

	// After a cluster rebuild the backup restores the values.
	fake.stored = nil
	stdout, _, code = run(t, "app", "add", "hello", helloArtifact, "--kubeconfig", kubeconfig)
	if code != 0 || fake.added[2].Secrets["db-password"] != password {
		t.Fatalf("code %d, secrets %v", code, fake.added[2].Secrets)
	}
	if !strings.Contains(stdout, "secret db-password: restored from the backup") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestAppAddWithoutSecrets(t *testing.T) {
	kubeconfig, backup := appTestEnv(t)
	fake := &fakeApps{app: appWithSecrets("web")}
	fake.install(t)
	_, stderr, code := run(t, "app", "add", "web", "oci://ghcr.io/o/web-deploy:main", "--kubeconfig", kubeconfig)
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if len(fake.added[0].Secrets) != 0 {
		t.Errorf("secrets %v", fake.added[0].Secrets)
	}
	if _, err := os.Stat(backup.Path("web")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an app without secrets needs no backup: %v", err)
	}
}

func TestAppAddErrors(t *testing.T) {
	kubeconfig, _ := appTestEnv(t)
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
			fake := &fakeApps{app: tt.app, fetchErr: tt.fetchErr}
			fake.install(t)
			_, stderr, code := run(t, append(append([]string{"app", "add"}, tt.args...), "--kubeconfig", kubeconfig)...)
			if code == 0 || !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("code %d, stderr %q, want %q", code, stderr, tt.wantErr)
			}
			if len(fake.added) != 0 {
				t.Error("nothing may be added")
			}
		})
	}
}

func TestAppRm(t *testing.T) {
	kubeconfig, backup := appTestEnv(t)
	if err := backup.Save("hello", map[string]string{"db-password": "x"}); err != nil {
		t.Fatal(err)
	}
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
			wantOut: "The secret backup stays in " + backup.Path("hello")},
		{name: "declined", args: []string{"hello"}, stdin: "n\n", found: true, wantCode: 1, wantErr: "aborted"},
		{name: "unknown app", args: []string{"nope", "--yes"}, wantCode: 1, wantRemoved: 1, wantErr: "app nope does not exist"},
		{name: "invalid name", args: []string{"a_b", "--yes"}, wantCode: 1, wantErr: "DNS label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeApps{found: tt.found}
			fake.install(t)
			cmd := New(images)
			var out, errOut strings.Builder
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetIn(strings.NewReader(tt.stdin))
			cmd.SetArgs(append(append([]string{"app", "rm"}, tt.args...), "--kubeconfig", kubeconfig))
			code := Execute(context.Background(), cmd)
			if code != tt.wantCode {
				t.Errorf("exit code %d, want %d: %s", code, tt.wantCode, errOut.String())
			}
			if len(fake.removed) != tt.wantRemoved {
				t.Errorf("remove called %d times, want %d", len(fake.removed), tt.wantRemoved)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantOut, out.String())
			}
			if !strings.Contains(errOut.String(), tt.wantErr) {
				t.Errorf("stderr lacks %q: %s", tt.wantErr, errOut.String())
			}
			if tt.wantRemoved == 1 && !strings.Contains(out.String(), "including all its volumes") {
				t.Errorf("the warning is missing:\n%s", out.String())
			}
		})
	}
}

func TestAppAddAndRmPublishTheHostName(t *testing.T) {
	kubeconfig, _ := appTestEnv(t)
	fake := &fakeApps{app: appWithSecrets("greeter"), found: true}
	exposedApps(fake)
	fake.install(t)
	t.Setenv(envCloudflareToken, "cf-secret-token")

	stdout, stderr, code := run(t, "app", "add", "greeter", "oci://ghcr.io/o/greeter:main", "--kubeconfig", kubeconfig)
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	want := "greeter-dev.example.com -> t-1.cfargotunnel.com in zone-1"
	if len(fake.api.records) != 1 || fake.api.records[0] != want {
		t.Errorf("records %v, want %q", fake.api.records, want)
	}
	if !strings.Contains(stdout, "DNS greeter-dev.example.com -> t-1.cfargotunnel.com: created") {
		t.Errorf("stdout lacks the record:\n%s", stdout)
	}

	if _, stderr, code = run(t, "app", "rm", "greeter", "--yes", "--kubeconfig", kubeconfig); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if len(fake.api.deletedRecords) != 1 || fake.api.deletedRecords[0] != "greeter-dev.example.com" {
		t.Errorf("deleted %v", fake.api.deletedRecords)
	}
}

func TestAppAddWithoutExposure(t *testing.T) {
	kubeconfig, _ := appTestEnv(t)
	tests := map[string]struct {
		setup   func(*fakeApps)
		token   string
		wantOut string
	}{
		"cluster not exposed": {setup: func(*fakeApps) {}, token: "cf-secret-token"},
		"no token":            {setup: exposedApps, wantOut: "DNS: skipped"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeApps{app: appWithSecrets("greeter"), api: &fakeCloudflare{}}
			tt.setup(fake)
			fake.install(t)
			t.Setenv(envCloudflareToken, tt.token)

			stdout, stderr, code := run(t, "app", "add", "greeter", "oci://ghcr.io/o/greeter:main", "--kubeconfig", kubeconfig)
			if code != 0 {
				t.Fatalf("exit code %d: %s", code, stderr)
			}
			if len(fake.api.records) != 0 {
				t.Errorf("no record may be written: %v", fake.api.records)
			}
			if tt.wantOut != "" && !strings.Contains(stdout, tt.wantOut) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantOut, stdout)
			}
		})
	}
}
