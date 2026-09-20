package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cloudflare"
	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
)

// fakeCloudflare answers like the Cloudflare API without talking to it.
type fakeCloudflare struct {
	verifyErr      error
	zoneErr        error
	zoneFor        []string
	records        []string
	deletedRecords []string
	accounts       []string
	existing       *cloudflare.Tunnel
	created        []string
	deleted        []string
	accountID      string
}

func (f *fakeCloudflare) VerifyToken(context.Context) error { return f.verifyErr }

func (f *fakeCloudflare) AccountID(context.Context) (string, error) {
	if len(f.accounts) != 1 {
		return "", errors.New("set CF_ACCOUNT_ID")
	}
	return f.accounts[0], nil
}

func (f *fakeCloudflare) FindTunnel(_ context.Context, _, name string) (*cloudflare.Tunnel, error) {
	if f.existing != nil && f.existing.Name == name {
		return f.existing, nil
	}
	return nil, nil
}

func (f *fakeCloudflare) CreateTunnel(_ context.Context, account, name string) (*cloudflare.Tunnel, []byte, error) {
	f.created = append(f.created, name)
	f.accountID = account
	tunnel := &cloudflare.Tunnel{ID: "new-tunnel", Name: name}
	f.existing = tunnel
	return tunnel, []byte(`{"TunnelID":"new-tunnel"}`), nil
}

func (f *fakeCloudflare) DeleteTunnel(_ context.Context, _, id string) error {
	f.deleted = append(f.deleted, id)
	f.existing = nil
	return nil
}

func (f *fakeCloudflare) ZoneFor(_ context.Context, name string) (*cloudflare.Zone, error) {
	if f.zoneErr != nil {
		return nil, f.zoneErr
	}
	f.zoneFor = append(f.zoneFor, name)
	return &cloudflare.Zone{ID: "zone-1", Name: name}, nil
}

func (f *fakeCloudflare) EnsureRecord(_ context.Context, zone, name, target string) (cloudflare.Action, error) {
	f.records = append(f.records, name+" -> "+target+" in "+zone)
	return cloudflare.Created, nil
}

func (f *fakeCloudflare) DeleteRecord(_ context.Context, _, name string) (bool, error) {
	f.deletedRecords = append(f.deletedRecords, name)
	return true, nil
}

// exposeHarness is a cluster that has a domain but is not exposed yet, with a token at hand.
func exposeHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.cluster.settings = cluster.Settings{Domain: "example.com", HostSuffix: "-dev"}
	h.env[ops.EnvCloudflareToken] = "cf-secret-token"
	return h
}

func TestInitExposeCreatesTunnel(t *testing.T) {
	t.Parallel()
	h := exposeHarness(t)

	stdout, stderr, code := h.run(t, "init", "expose", "--yes")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "cf-secret-token") {
		t.Fatal("the token appears in the output")
	}
	if len(h.api.created) != 1 || h.api.created[0] != "shelf-dev" {
		t.Errorf("created %v; the tunnel is named after the host suffix", h.api.created)
	}
	if len(h.cluster.exposed) != 1 {
		t.Fatalf("expose called %d times", len(h.cluster.exposed))
	}
	got := h.cluster.exposed[0]
	if got.TunnelID != "new-tunnel" || string(got.Credentials) != `{"TunnelID":"new-tunnel"}` {
		t.Errorf("options %+v", got)
	}
	for _, want := range []string{"hosts     <app>-dev.example.com", "tunnel    shelf-dev",
		"tunnel shelf-dev created", "target    new-tunnel.cfargotunnel.com"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestInitExposeReusesTunnel(t *testing.T) {
	t.Parallel()
	h := exposeHarness(t)
	h.api.existing = &cloudflare.Tunnel{ID: "old-tunnel", Name: "shelf-dev"}
	h.cluster.tunnelCreds = []byte(`{"TunnelID":"old-tunnel","TunnelSecret":"x"}`)

	_, stderr, code := h.run(t, "init", "expose", "--yes")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if len(h.api.created) != 0 || len(h.api.deleted) != 0 {
		t.Errorf("created %v, deleted %v; an existing tunnel with credentials is reused",
			h.api.created, h.api.deleted)
	}
	if got := h.cluster.exposed[0]; got.TunnelID != "old-tunnel" || got.Credentials != nil {
		t.Errorf("options %+v; the stored credentials must be kept", got)
	}
}

func TestInitExposeReplacesTunnelWithoutCredentials(t *testing.T) {
	t.Parallel()
	h := exposeHarness(t)
	h.api.existing = &cloudflare.Tunnel{ID: "old-tunnel", Name: "shelf-dev"}

	stdout, _, code := h.run(t, "init", "expose", "--yes")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if len(h.api.deleted) != 1 || h.api.deleted[0] != "old-tunnel" || len(h.api.created) != 1 {
		t.Errorf("deleted %v, created %v", h.api.deleted, h.api.created)
	}
	if !strings.Contains(stdout, "has to be replaced") || !strings.Contains(stdout, "tunnel shelf-dev replaced") {
		t.Errorf("stdout does not explain the replacement:\n%s", stdout)
	}
}

func TestInitExposeDeclined(t *testing.T) {
	t.Parallel()
	h := exposeHarness(t)
	h.api.existing = &cloudflare.Tunnel{ID: "old-tunnel", Name: "shelf-dev"}

	_, stderr, code := h.runWithInput(t, "n\n", "init", "expose")
	if code != 1 {
		t.Fatalf("exit code %d", code)
	}
	if len(h.api.deleted) != 0 || len(h.cluster.exposed) != 0 {
		t.Error("nothing may change when the replacement is declined")
	}
	if !strings.Contains(stderr, "aborted") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestInitExposeErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		noToken bool
		setup   func(*harness)
		wantErr string
	}{
		{name: "no token", noToken: true, wantErr: "CF_API_TOKEN is not set"},
		{
			name:    "cluster without domain",
			setup:   func(h *harness) { h.cluster.settings = cluster.Settings{} },
			wantErr: "shelf init cluster",
		},
		{
			name:    "token refused",
			setup:   func(h *harness) { h.api.verifyErr = errors.New("Invalid request headers (code 6003)") },
			wantErr: "CF_API_TOKEN was refused",
		},
		{
			name:    "several accounts",
			setup:   func(h *harness) { h.api.accounts = []string{"acc-1", "acc-2"} },
			wantErr: "CF_ACCOUNT_ID",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := exposeHarness(t)
			if tt.noToken {
				delete(h.env, ops.EnvCloudflareToken)
			}
			if tt.setup != nil {
				tt.setup(h)
			}
			_, stderr, code := h.run(t, "init", "expose", "--yes")
			if code == 0 || !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("exit code %d, stderr %q, want %q", code, stderr, tt.wantErr)
			}
			if len(h.cluster.exposed) != 0 {
				t.Error("nothing may be exposed")
			}
		})
	}
}

func TestInitExposeAccountFromEnvironment(t *testing.T) {
	t.Parallel()
	h := exposeHarness(t)
	h.api.accounts = []string{"acc-1", "acc-2"}
	h.env[ops.EnvCloudflareAccount] = "acc-2"

	_, stderr, code := h.run(t, "init", "expose", "--yes", "--tunnel", "my-tunnel")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if h.api.accountID != "acc-2" {
		t.Errorf("account %q", h.api.accountID)
	}
	if len(h.api.created) != 1 || h.api.created[0] != "my-tunnel" {
		t.Errorf("created %v", h.api.created)
	}
}

// TestInitExposePublishesRunningApps checks that an exposure set up after the apps publishes
// their host names, instead of waiting for the next `shelf app add`.
func TestInitExposePublishesRunningApps(t *testing.T) {
	t.Parallel()
	h := exposeHarness(t)
	h.cluster.apps = []string{"greeter", "shop"}

	stdout, stderr, code := h.run(t, "init", "expose", "--yes")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	want := []string{
		"greeter-dev.example.com -> new-tunnel.cfargotunnel.com in zone-1",
		"shop-dev.example.com -> new-tunnel.cfargotunnel.com in zone-1",
	}
	if len(h.api.records) != len(want) {
		t.Fatalf("records %v, want %v", h.api.records, want)
	}
	for i, w := range want {
		if h.api.records[i] != w {
			t.Errorf("record %d = %q, want %q", i, h.api.records[i], w)
		}
	}
	if !strings.Contains(stdout, "DNS greeter-dev.example.com") {
		t.Errorf("stdout lacks the published host:\n%s", stdout)
	}
}
