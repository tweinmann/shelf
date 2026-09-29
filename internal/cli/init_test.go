package cli

import (
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
)

func TestInitCluster(t *testing.T) {
	t.Parallel()
	const platform = "oci://shelf-registry:5000/shelf/platform:dev"
	const chart = "oci://shelf-registry:5000/shelf/charts/shelf-app:0.0.0-dev"
	base := []string{"init", "cluster", "--domain", "dev.local"}

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
			t.Parallel()
			h := newHarness(t)
			if tt.devVersion {
				h.version = "dev"
			}
			stdout, stderr, code := h.runWithInput(t, tt.stdin, append(append([]string{}, base...), tt.args...)...)

			if code != tt.wantCode {
				t.Errorf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout, stderr)
			}
			if len(h.cluster.installed) != tt.wantCalls {
				t.Fatalf("install called %d times, want %d", len(h.cluster.installed), tt.wantCalls)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			if tt.wantErr != "" && !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr lacks %q:\n%s", tt.wantErr, stderr)
			}
			if tt.wantCalls == 1 {
				got := h.cluster.installed[0]
				if got.Platform.String() != platform || got.Settings.Chart.String() != chart ||
					got.Settings.InsecureRegistry != tt.wantInsec || got.Settings.Domain != "dev.local" {
					t.Errorf("options %+v", got)
				}
				if h.cluster.host != tt.wantHost {
					t.Errorf("host %s, want %s", h.cluster.host, tt.wantHost)
				}
				if got.Timeout <= 0 || got.Report == nil {
					t.Errorf("options not set: %+v", got)
				}
			}
		})
	}
}

func TestInitClusterDefaultPlatform(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.version = "v0.3.0"

	_, stderr, code := h.run(t, "init", "cluster", "--yes", "--domain", "example.com")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if got := h.cluster.installed[0].Platform.String(); got != DefaultPlatformRepository+":v0.3.0" {
		t.Errorf("platform %s", got)
	}
	if got := h.cluster.installed[0].Settings.Chart.String(); got != DefaultChartRepository+":0.3.0" {
		t.Errorf("chart %s", got)
	}
}

func TestInitClusterSettings(t *testing.T) {
	t.Parallel()
	base := []string{"init", "cluster", "--yes"}

	tests := []struct {
		name    string
		args    []string
		wantErr string
		wantOut string
	}{
		{name: "no domain", wantErr: "--domain"},
		{name: "invalid domain", args: []string{"--domain", "Not A Domain"}, wantErr: "--domain"},
		{name: "invalid suffix", args: []string{"--domain", "example.com", "--host-suffix", "-a_b"}, wantErr: "--host-suffix"},
		{name: "invalid chart", args: []string{"--domain", "example.com", "--chart", "oci://x"}, wantErr: "--chart"},
		{name: "valid", args: []string{"--domain", "example.com", "--host-suffix", "-dev"},
			wantOut: "hosts     <app>-dev.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			// A registry login is a connection now; one left in the shell is not read.
			h.env[envRegistryUser], h.env[envRegistryToken] = "tobi", "secret-token"
			stdout, stderr, code := h.run(t, append(append([]string{}, base...), tt.args...)...)
			if strings.Contains(stdout+stderr, "secret-token") {
				t.Fatal("the token appears in the output")
			}
			if tt.wantErr != "" {
				if code == 0 || !strings.Contains(stderr, tt.wantErr) || len(h.cluster.installed) != 0 {
					t.Fatalf("code %d, stderr %q, calls %d", code, stderr, len(h.cluster.installed))
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit code %d: %s", code, stderr)
			}
			if !strings.Contains(stdout, tt.wantOut) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantOut, stdout)
			}
		})
	}
}

// TestInitClusterRefusesToMoveTheApps covers the mistake this guard exists for: a second
// `init cluster` with a different domain takes apps off the name they answer under, and the DNS
// records under the old names stay behind pointing at a tunnel that no longer routes them.
func TestInitClusterRefusesToMoveTheApps(t *testing.T) {
	t.Parallel()
	settings := cluster.Settings{Domain: "tobile.ch", HostSuffix: "-dev"}
	greeter := cluster.AppState{Name: "greeter", Tunnel: "t-1"}
	shop := cluster.AppState{Name: "shop"}
	blog := cluster.AppState{Name: "blog", Domain: "blog.example", Tunnel: "t-2"}
	tests := []struct {
		name     string
		settings cluster.Settings
		apps     []cluster.AppState
		args     []string
		wantErr  string
	}{
		{
			name: "a different domain while apps run", settings: settings, apps: []cluster.AppState{greeter},
			args: []string{"--domain", "dev.local"},
			wantErr: "this cluster serves <app>-dev.tobile.ch; changing it to <app>.dev.local moves app greeter, " +
				"and the records under the old names stay behind",
		},
		{
			name: "a different host suffix while apps run", settings: settings,
			apps: []cluster.AppState{greeter, shop, blog},
			args: []string{"--domain", "tobile.ch"}, wantErr: "moves 3 apps",
		},
		{
			name: "only apps on the cluster's domain move", settings: settings,
			apps: []cluster.AppState{shop, blog},
			args: []string{"--domain", "other.example", "--host-suffix", "-dev"}, wantErr: "moves app shop.",
		},
		{
			name: "an app with a domain of its own stays", settings: settings, apps: []cluster.AppState{blog},
			args: []string{"--domain", "other.example", "--host-suffix", "-dev"},
		},
		{
			name: "asked for it", settings: settings, apps: []cluster.AppState{greeter},
			args: []string{"--domain", "dev.local", "--move-hosts"},
		},
		{
			name: "the same names", settings: settings, apps: []cluster.AppState{greeter},
			args: []string{"--domain", "tobile.ch", "--host-suffix", "-dev"},
		},
		{
			name: "no apps to move", settings: settings,
			args: []string{"--domain", "dev.local"},
		},
		{
			name: "a cluster without a domain", apps: []cluster.AppState{greeter},
			args: []string{"--domain", "dev.local"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.cluster.settings = tt.settings
			h.cluster.states = tt.apps
			args := append([]string{"init", "cluster", "--yes"}, tt.args...)
			_, stderr, code := h.run(t, args...)

			if tt.wantErr == "" {
				if code != 0 {
					t.Fatalf("exit code %d: %s", code, stderr)
				}
				if len(h.cluster.installed) != 1 {
					t.Error("nothing was installed")
				}
				return
			}
			if code == 0 || !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("code %d, stderr %q, want %q", code, stderr, tt.wantErr)
			}
			if len(h.cluster.installed) != 0 {
				t.Error("the platform was installed anyway")
			}
			if !strings.Contains(stderr, "--move-hosts") {
				t.Errorf("the error does not say how to do it on purpose: %s", stderr)
			}
		})
	}
}
