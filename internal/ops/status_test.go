package ops

import (
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
