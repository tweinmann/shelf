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
	base := []string{"init", "cluster"}

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
					got.Settings.InsecureRegistry != tt.wantInsec || got.PinDomain != "" {
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

	_, stderr, code := h.run(t, "init", "cluster", "--yes")
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
		{name: "a cluster has no domain", args: []string{"--domain", "example.com"}, wantErr: "unknown flag: --domain"},
		{name: "invalid suffix", args: []string{"--host-suffix", "-a_b"}, wantErr: "--host-suffix"},
		{name: "invalid chart", args: []string{"--chart", "oci://x"}, wantErr: "--chart"},
		{name: "valid", args: []string{"--host-suffix", "-dev"},
			wantOut: "hosts     <app>-dev.<domain>, or <app>.shelf.internal without a domain"},
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
// `init cluster` with a different host suffix takes the apps with a domain off the name they
// answer under, and the DNS records under the old names stay behind pointing at a tunnel that no
// longer routes them.
func TestInitClusterRefusesToMoveTheApps(t *testing.T) {
	t.Parallel()
	chart := cluster.Artifact{URL: "oci://ghcr.io/tweinmann/shelf/charts/shelf-app", Tag: "0.5.0"}
	settings := cluster.Settings{HostSuffix: "-dev", Chart: chart}
	greeter := cluster.AppState{Name: "greeter", Domain: "tobile.ch", Tunnel: "t-1"}
	shop := cluster.AppState{Name: "shop", Domain: "tobile.ch"}
	blog := cluster.AppState{Name: "blog", Domain: "blog.example", Tunnel: "t-2"}
	notes := cluster.AppState{Name: "notes", Tunnel: "t-3"}
	tests := []struct {
		name     string
		settings cluster.Settings
		apps     []cluster.AppState
		args     []string
		wantErr  string
	}{
		{
			name: "a different host suffix while apps run", settings: settings, apps: []cluster.AppState{greeter},
			wantErr: "this cluster serves <app>-dev.<domain>; changing it to <app>.<domain> moves app greeter, " +
				"and the records under the old names stay behind",
		},
		{
			name: "every app with a domain moves", settings: settings,
			apps: []cluster.AppState{greeter, shop, blog}, wantErr: "moves 3 apps",
		},
		{
			name: "an app without a domain stays", settings: settings, apps: []cluster.AppState{{Name: "notes"}},
		},
		{
			name: "asked for it", settings: settings, apps: []cluster.AppState{greeter},
			args: []string{"--move-hosts"},
		},
		{
			name: "the same names", settings: settings, apps: []cluster.AppState{greeter},
			args: []string{"--host-suffix", "-dev"},
		},
		{name: "no apps to move", settings: settings},
		{name: "the first installation", apps: []cluster.AppState{greeter}},
		{
			name: "an app about to keep the cluster's former domain", apps: []cluster.AppState{notes},
			settings: cluster.Settings{HostSuffix: "-dev", Chart: chart, LegacyDomain: "tobile.ch"},
			args:     []string{"--host-suffix", "-test"},
			wantErr:  "moves app notes, and the records under the old names stay behind",
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

// TestInitClusterPinsTheFormerDomain covers a cluster from when clusters had a domain: the apps
// that answered under a public one keep it as a domain of their own, while dev.local, which no
// record can point at, is not kept.
func TestInitClusterPinsTheFormerDomain(t *testing.T) {
	t.Parallel()
	chart := cluster.Artifact{URL: "oci://ghcr.io/tweinmann/shelf/charts/shelf-app", Tag: "0.5.0"}
	for legacy, want := range map[string]string{"tobile.ch": "tobile.ch", "dev.local": "", "": ""} {
		h := newHarness(t)
		h.cluster.settings = cluster.Settings{HostSuffix: "-dev", Chart: chart, LegacyDomain: legacy}
		_, stderr, code := h.run(t, "init", "cluster", "--yes", "--host-suffix", "-dev")
		if code != 0 {
			t.Fatalf("%q: exit code %d: %s", legacy, code, stderr)
		}
		if got := h.cluster.installed[0].PinDomain; got != want {
			t.Errorf("former domain %q: pinned %q, want %q", legacy, got, want)
		}
	}
}
