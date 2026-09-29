package ops

import (
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
)

// TestComponents covers where each part of an app is said to answer. The rule that matters: a
// link is offered only where the app itself has one, so the two can never point at names of
// different reachability.
func TestComponents(t *testing.T) {
	t.Parallel()
	list := []cluster.Component{
		{Name: "check", Phase: cluster.PhaseWorking},
		{Name: "db", Ports: []cluster.Port{{Name: "main", Number: 5432}}, Phase: cluster.PhaseReady},
		{Name: "web", Ports: []cluster.Port{{Name: "main", Number: 80}}, Path: "/", Phase: cluster.PhaseReady},
		{Name: "api", Ports: []cluster.Port{{Name: "http", Number: 3000}}, Path: "/api", Phase: cluster.PhaseReady},
	}

	tests := map[string]struct {
		host     string
		exposed  bool
		wantURL  map[string]string
		wantAddr map[string]string
	}{
		"exposed": {
			host: "hello-dev.example.com", exposed: true,
			wantURL: map[string]string{
				"web": "https://hello-dev.example.com/",
				"api": "https://hello-dev.example.com/api",
			},
			wantAddr: map[string]string{
				"check": "", "db": "db:5432",
				"web": "hello-dev.example.com/", "api": "hello-dev.example.com/api",
			},
		},
		"a host but no tunnel": {
			host:    "hello.example.com",
			wantURL: map[string]string{},
			wantAddr: map[string]string{
				"check": "", "db": "db:5432",
				"web": "hello.example.com/", "api": "hello.example.com/api",
			},
		},
		"a cluster without a domain": {
			wantURL: map[string]string{},
			wantAddr: map[string]string{
				"check": "", "db": "db:5432", "web": "", "api": "",
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := components(list, tt.host, tt.exposed)
			if len(got) != len(list) {
				t.Fatalf("got %d components, want %d", len(got), len(list))
			}
			for _, c := range got {
				if c.URL != tt.wantURL[c.Name] {
					t.Errorf("%s: URL %q, want %q", c.Name, c.URL, tt.wantURL[c.Name])
				}
				if c.Address != tt.wantAddr[c.Name] {
					t.Errorf("%s: address %q, want %q", c.Name, c.Address, tt.wantAddr[c.Name])
				}
				if want := c.Path == ""; c.Internal != want {
					t.Errorf("%s: internal %v, want %v", c.Name, c.Internal, want)
				}
			}
		})
	}
}

// A component that listens on several ports names each of them, because a sibling has to pick.
func TestSiblingAddress(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		component cluster.Component
		want      string
	}{
		"no ports": {cluster.Component{Name: "check"}, ""},
		"one port": {
			cluster.Component{Name: "db", Ports: []cluster.Port{{Name: "main", Number: 5432}}},
			"db:5432",
		},
		"two ports": {
			cluster.Component{Name: "api", Ports: []cluster.Port{
				{Name: "http", Number: 8080}, {Name: "metrics", Number: 9090},
			}},
			"api:8080, api:9090",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := siblingAddress(tt.component); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAppAddress covers where an app is said to answer: a named tunnel links the app's own host
// name, but only on a public domain; a quick tunnel links whatever name cloudflared was given,
// on any domain, once it has one.
func TestAppAddress(t *testing.T) {
	t.Parallel()
	settings := cluster.Settings{Domain: "dev.local", HostSuffix: "-dev"}
	tests := map[string]struct {
		state    cluster.AppState
		wantHost string
		wantURL  string
	}{
		"not exposed": {
			state:    cluster.AppState{Name: "hello"},
			wantHost: "hello-dev.dev.local",
		},
		"named tunnel on a reserved domain": {
			state:    cluster.AppState{Name: "hello", Tunnel: "t-1"},
			wantHost: "hello-dev.dev.local",
		},
		"named tunnel on a public domain": {
			state:    cluster.AppState{Name: "hello", Tunnel: "t-1", Domain: "example.com"},
			wantHost: "hello-dev.example.com",
			wantURL:  "https://hello-dev.example.com/",
		},
		"quick tunnel without an address yet": {
			state:    cluster.AppState{Name: "hello", Quick: true},
			wantHost: "hello-dev.dev.local",
		},
		"quick tunnel": {
			state:    cluster.AppState{Name: "hello", Quick: true, QuickURL: "https://some-words.trycloudflare.com/"},
			wantHost: "some-words.trycloudflare.com",
			wantURL:  "https://some-words.trycloudflare.com/",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a := app(tt.state, settings)
			if a.Host != tt.wantHost || a.URL != tt.wantURL {
				t.Errorf("host %q, URL %q; want %q, %q", a.Host, a.URL, tt.wantHost, tt.wantURL)
			}
			comps := components([]cluster.Component{{Name: "web", Path: "/app"}}, a.Host, a.URL != "")
			if want := strings.TrimSuffix(tt.wantURL, "/") + "/app"; tt.wantURL != "" && comps[0].URL != want {
				t.Errorf("component URL %q, want %q", comps[0].URL, want)
			}
		})
	}
}
