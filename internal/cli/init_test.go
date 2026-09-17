package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/cluster"
)

const testKubeconfig = `apiVersion: v1
kind: Config
current-context: dev
contexts:
- name: dev
  context: {cluster: dev, user: dev}
- name: other
  context: {cluster: other, user: dev}
clusters:
- name: dev
  cluster: {server: "https://127.0.0.1:6445"}
- name: other
  cluster: {server: "https://mini.example:6443"}
users:
- name: dev
  user: {token: t}
`

// fakeInstall records calls instead of touching a cluster.
type fakeInstall struct {
	calls []cluster.Options
	host  string
}

func (f *fakeInstall) install(_ context.Context, cfg *rest.Config, opts cluster.Options) error {
	f.calls = append(f.calls, opts)
	f.host = cfg.Host
	return nil
}

func TestInitCluster(t *testing.T) {
	kubeconfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kubeconfig, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	const platform = "oci://shelf-registry:5000/shelf/platform:dev"
	const chart = "oci://shelf-registry:5000/shelf/charts/shelf-app:0.0.0-dev"
	base := []string{"init", "cluster", "--kubeconfig", kubeconfig, "--domain", "dev.local"}

	tests := []struct {
		name       string
		args       []string
		stdin      string
		wantCode   int
		wantCalls  int
		wantOut    []string
		wantErr    string
		wantHost   string
		wantInsec  bool
		devVersion bool
	}{
		{
			name:     "confirmed",
			args:     []string{"--platform", platform, "--chart", chart},
			stdin:    "y\n",
			wantCode: 0, wantCalls: 1,
			wantOut:  []string{"context   dev", "server    https://127.0.0.1:6445", "platform  " + platform, "Proceed? [y/N]"},
			wantHost: "https://127.0.0.1:6445",
		},
		{
			name:     "declined",
			args:     []string{"--platform", platform, "--chart", chart},
			stdin:    "n\n",
			wantCode: 1, wantCalls: 0,
			wantErr: "aborted",
		},
		{
			name:     "no answer",
			args:     []string{"--platform", platform, "--chart", chart},
			stdin:    "",
			wantCode: 1, wantCalls: 0,
			wantErr: "aborted",
		},
		{
			name:     "yes flag and other context",
			args:     []string{"--platform", platform, "--chart", chart, "--insecure-registry", "--yes", "--context", "other"},
			wantCode: 0, wantCalls: 1,
			wantOut:   []string{"context   other"},
			wantHost:  "https://mini.example:6443",
			wantInsec: true,
		},
		{
			name:     "unknown context",
			args:     []string{"--platform", platform, "--chart", chart, "--yes", "--context", "nope"},
			wantCode: 1, wantCalls: 0,
			wantErr: "nope",
		},
		{
			name:     "invalid platform",
			args:     []string{"--platform", "ghcr.io/x/platform:v1", "--yes"},
			wantCode: 1, wantCalls: 0,
			wantErr: "must start with oci://",
		},
		{
			name:       "dev build needs --platform",
			args:       []string{"--yes"},
			devVersion: true,
			wantCode:   1, wantCalls: 0,
			wantErr: "pass --platform",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeInstall{}
			restore := installCluster
			installCluster = fake.install
			t.Cleanup(func() { installCluster = restore })
			oldVersion := Version
			t.Cleanup(func() { Version = oldVersion })
			Version = "v0.0.0-test"
			if tt.devVersion {
				Version = "" // go test binaries report "dev"
			}

			cmd := New(images)
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetIn(strings.NewReader(tt.stdin))
			cmd.SetArgs(append(append([]string{}, base...), tt.args...))
			code := Execute(context.Background(), cmd)

			if code != tt.wantCode {
				t.Errorf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, out.String(), errOut.String())
			}
			if len(fake.calls) != tt.wantCalls {
				t.Fatalf("install called %d times, want %d", len(fake.calls), tt.wantCalls)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("stdout lacks %q:\n%s", want, out.String())
				}
			}
			if tt.wantErr != "" && !strings.Contains(errOut.String(), tt.wantErr) {
				t.Errorf("stderr lacks %q:\n%s", tt.wantErr, errOut.String())
			}
			if tt.wantCalls == 1 {
				got := fake.calls[0]
				if got.Platform.String() != platform || got.Settings.Chart.String() != chart ||
					got.Settings.InsecureRegistry != tt.wantInsec || got.Settings.Domain != "dev.local" {
					t.Errorf("options %+v", got)
				}
				if fake.host != tt.wantHost {
					t.Errorf("host %s, want %s", fake.host, tt.wantHost)
				}
				if got.Timeout <= 0 || got.Out == nil {
					t.Errorf("options not set: %+v", got)
				}
			}
		})
	}
}

func TestInitClusterDefaultPlatform(t *testing.T) {
	kubeconfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kubeconfig, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeInstall{}
	restore, oldVersion := installCluster, Version
	installCluster, Version = fake.install, "v0.3.0"
	t.Cleanup(func() { installCluster, Version = restore, oldVersion })

	_, stderr, code := run(t, "init", "cluster", "--kubeconfig", kubeconfig, "--yes", "--domain", "example.com")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if got := fake.calls[0].Platform.String(); got != DefaultPlatformRepository+":v0.3.0" {
		t.Errorf("platform %s", got)
	}
	if got := fake.calls[0].Settings.Chart.String(); got != DefaultChartRepository+":0.3.0" {
		t.Errorf("chart %s", got)
	}
}

func TestInitClusterSettings(t *testing.T) {
	kubeconfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kubeconfig, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeInstall{}
	restore, oldVersion := installCluster, Version
	installCluster, Version = fake.install, "v0.3.0"
	t.Cleanup(func() { installCluster, Version = restore, oldVersion })
	base := []string{"init", "cluster", "--kubeconfig", kubeconfig, "--yes"}

	tests := []struct {
		name      string
		args      []string
		user      string
		token     string
		wantErr   string
		wantLogin string
		wantOut   string
	}{
		{name: "no domain", wantErr: "--domain"},
		{name: "invalid domain", args: []string{"--domain", "Not A Domain"}, wantErr: "--domain"},
		{name: "invalid chart", args: []string{"--domain", "example.com", "--chart", "oci://x"}, wantErr: "--chart"},
		{name: "token without user", args: []string{"--domain", "example.com"}, token: "secret-token", wantErr: "GHCR_USERNAME"},
		{name: "login", args: []string{"--domain", "example.com"}, user: "tobi", token: "secret-token",
			wantLogin: "tobi", wantOut: "registry  ghcr.io as tobi"},
		{name: "no login", args: []string{"--domain", "example.com"}, wantOut: "login unchanged"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.calls = nil
			t.Setenv("GHCR_USERNAME", tt.user)
			t.Setenv("GHCR_TOKEN", tt.token)
			stdout, stderr, code := run(t, append(append([]string{}, base...), tt.args...)...)
			if strings.Contains(stdout+stderr, "secret-token") {
				t.Fatal("the token appears in the output")
			}
			if tt.wantErr != "" {
				if code == 0 || !strings.Contains(stderr, tt.wantErr) || len(fake.calls) != 0 {
					t.Fatalf("code %d, stderr %q, calls %d", code, stderr, len(fake.calls))
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit code %d: %s", code, stderr)
			}
			if !strings.Contains(stdout, tt.wantOut) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantOut, stdout)
			}
			auth := fake.calls[0].Registry
			switch {
			case tt.wantLogin == "" && auth != nil:
				t.Errorf("unexpected login %+v", auth.Username)
			case tt.wantLogin != "" && (auth == nil || auth.Username != tt.wantLogin || auth.Token != tt.token):
				t.Errorf("login not passed on")
			}
		})
	}
}
