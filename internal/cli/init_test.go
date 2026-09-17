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
	base := []string{"init", "cluster", "--kubeconfig", kubeconfig}

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
			args:     []string{"--platform", platform},
			stdin:    "y\n",
			wantCode: 0, wantCalls: 1,
			wantOut:  []string{"context   dev", "server    https://127.0.0.1:6445", "platform  " + platform, "Proceed? [y/N]"},
			wantHost: "https://127.0.0.1:6445",
		},
		{
			name:     "declined",
			args:     []string{"--platform", platform},
			stdin:    "n\n",
			wantCode: 1, wantCalls: 0,
			wantErr: "aborted",
		},
		{
			name:     "no answer",
			args:     []string{"--platform", platform},
			stdin:    "",
			wantCode: 1, wantCalls: 0,
			wantErr: "aborted",
		},
		{
			name:     "yes flag and other context",
			args:     []string{"--platform", platform, "--insecure-registry", "--yes", "--context", "other"},
			wantCode: 0, wantCalls: 1,
			wantOut:   []string{"context   other"},
			wantHost:  "https://mini.example:6443",
			wantInsec: true,
		},
		{
			name:     "unknown context",
			args:     []string{"--platform", platform, "--yes", "--context", "nope"},
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
				if got.Platform.String() != platform || got.Platform.Insecure != tt.wantInsec {
					t.Errorf("platform %+v", got.Platform)
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

	_, stderr, code := run(t, "init", "cluster", "--kubeconfig", kubeconfig, "--yes")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if got := fake.calls[0].Platform.String(); got != DefaultPlatformRepository+":v0.3.0" {
		t.Errorf("platform %s", got)
	}
}
