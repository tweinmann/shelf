package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cloudflare"
	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
)

// fakeCloudflare answers like the Cloudflare API without talking to it. Tunnels live in
// accounts, as they do at Cloudflare, so an app that moves to another account gets a new one.
type fakeCloudflare struct {
	verifyErr error
	zoneErr   error
	accounts  []string
	// tunnels are the tunnels that exist, by account and name.
	tunnels map[string]*cloudflare.Tunnel
	next    int

	zoneFor        []string
	records        []string
	deletedRecords []string
	created        []string
	deleted        []string
}

func tunnelKey(account, name string) string { return account + "/" + name }

func (f *fakeCloudflare) VerifyToken(context.Context) error { return f.verifyErr }

func (f *fakeCloudflare) Zones(context.Context, string) ([]cloudflare.Zone, error) {
	return []cloudflare.Zone{{ID: "zone-example.com", Name: "example.com"}, {ID: "zone-shop.ch", Name: "shop.ch"}}, nil
}

func (f *fakeCloudflare) AccountID(context.Context) (string, error) {
	if len(f.accounts) != 1 {
		return "", errors.New("set CF_ACCOUNT_ID")
	}
	return f.accounts[0], nil
}

func (f *fakeCloudflare) FindTunnel(_ context.Context, account, name string) (*cloudflare.Tunnel, error) {
	return f.tunnels[tunnelKey(account, name)], nil
}

func (f *fakeCloudflare) CreateTunnel(_ context.Context, account, name string) (*cloudflare.Tunnel, []byte, error) {
	f.next++
	tunnel := &cloudflare.Tunnel{ID: fmt.Sprintf("tunnel-%d", f.next), Name: name}
	if f.tunnels == nil {
		f.tunnels = map[string]*cloudflare.Tunnel{}
	}
	f.tunnels[tunnelKey(account, name)] = tunnel
	f.created = append(f.created, tunnelKey(account, name))
	return tunnel, []byte(`{"AccountTag":"` + account + `","TunnelID":"` + tunnel.ID + `"}`), nil
}

func (f *fakeCloudflare) DeleteTunnel(_ context.Context, account, id string) error {
	for key, t := range f.tunnels {
		if t.ID == id && strings.HasPrefix(key, account+"/") {
			delete(f.tunnels, key)
			f.deleted = append(f.deleted, id)
			return nil
		}
	}
	return fmt.Errorf("tunnel %s is not in account %s", id, account)
}

func (f *fakeCloudflare) ZoneFor(_ context.Context, name string) (*cloudflare.Zone, error) {
	if f.zoneErr != nil {
		return nil, f.zoneErr
	}
	f.zoneFor = append(f.zoneFor, name)
	return &cloudflare.Zone{ID: "zone-" + name, Name: name}, nil
}

func (f *fakeCloudflare) EnsureRecord(_ context.Context, zone, name, target string) (cloudflare.Action, error) {
	f.records = append(f.records, name+" -> "+target+" in "+zone)
	return cloudflare.Created, nil
}

func (f *fakeCloudflare) DeleteRecord(_ context.Context, zone, name string) (bool, error) {
	f.deletedRecords = append(f.deletedRecords, name+" in "+zone)
	return true, nil
}

const greeterArtifact = "oci://ghcr.io/o/greeter:main"

// cloudflareHarness is a cluster with a public domain and the Cloudflare connection "tobile" in
// account acc-1. The token is gone from the environment once the connection exists: nothing but
// `shelf connection add` may read it.
func cloudflareHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t).public()
	h.app = appWithSecrets("greeter")
	h.cluster.found = true
	h.defineCloudflare(t, "tobile", "cf-secret-token", "")
	return h
}

func (h *harness) defineCloudflare(t *testing.T, name, token, account string) string {
	t.Helper()
	h.env[envCloudflareToken], h.env[envCloudflareAccount] = token, account
	defer func() { delete(h.env, envCloudflareToken); delete(h.env, envCloudflareAccount) }()
	return h.mustRun(t, "connection", "add", "cloudflare", name)
}

func (h *harness) defineRegistry(t *testing.T, name, user, token string) string {
	t.Helper()
	h.env[envRegistryUser], h.env[envRegistryToken] = user, token
	defer func() { delete(h.env, envRegistryUser); delete(h.env, envRegistryToken) }()
	return h.mustRun(t, "connection", "add", "registry", name)
}

// mustRun runs a command that has to succeed and returns what it printed. It fails the test if a
// token shows up anywhere in the output.
func (h *harness) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, code := h.run(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit code %d: %s", args, code, stderr)
	}
	for _, token := range []string{"cf-secret-token", "cf-other-token", "ghcr-secret-token", "ghcr-new-token"} {
		if strings.Contains(stdout+stderr, token) {
			t.Fatalf("%v: a token appears in the output:\n%s%s", args, stdout, stderr)
		}
	}
	return stdout
}

func (h *harness) lastAdded(t *testing.T) cluster.AppOptions {
	t.Helper()
	if len(h.cluster.added) == 0 {
		t.Fatal("nothing was added")
	}
	return h.cluster.added[len(h.cluster.added)-1]
}

func TestConnectionAddCloudflare(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	stdout := h.defineCloudflare(t, "tobile", "cf-secret-token", "")
	if !strings.Contains(stdout, "Cloudflare connection tobile: created, account acc-1") {
		t.Errorf("stdout:\n%s", stdout)
	}
	stored, err := h.conns.Cloudflare("tobile")
	if err != nil || stored == nil || *stored != (hostcfg.Cloudflare{Name: "tobile", Token: "cf-secret-token", Account: "acc-1"}) {
		t.Fatalf("stored %+v, %v", stored, err)
	}
	stdout = h.defineCloudflare(t, "tobile", "cf-other-token", "")
	if !strings.Contains(stdout, "Cloudflare connection tobile: updated") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if stored, _ := h.conns.Cloudflare("tobile"); stored.Token != "cf-other-token" {
		t.Errorf("the token was not replaced")
	}
}

func TestConnectionAddErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		setup   func(*harness)
		wantErr string
	}{
		{name: "no Cloudflare token", args: []string{"cloudflare", "tobile"}, wantErr: "needs CF_API_TOKEN"},
		{
			name: "token refused", args: []string{"cloudflare", "tobile"},
			env:     map[string]string{envCloudflareToken: "cf-secret-token"},
			setup:   func(h *harness) { h.api.verifyErr = errors.New("Invalid request headers (code 6003)") },
			wantErr: "the Cloudflare token was refused",
		},
		{
			name: "several accounts", args: []string{"cloudflare", "tobile"},
			env:     map[string]string{envCloudflareToken: "cf-secret-token"},
			setup:   func(h *harness) { h.api.accounts = []string{"acc-1", "acc-2"} },
			wantErr: "CF_ACCOUNT_ID",
		},
		{
			name: "invalid name", args: []string{"cloudflare", "To_bile"},
			env:     map[string]string{envCloudflareToken: "cf-secret-token"},
			wantErr: "must be a DNS label",
		},
		{
			name: "no registry token", args: []string{"registry", "ghcr"},
			env:     map[string]string{envRegistryUser: "tobi"},
			wantErr: "needs GHCR_USERNAME and GHCR_TOKEN",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			for k, v := range tt.env {
				h.env[k] = v
			}
			if tt.setup != nil {
				tt.setup(h)
			}
			_, stderr, code := h.run(t, append([]string{"connection", "add"}, tt.args...)...)
			if code == 0 || !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("exit code %d, stderr %q, want %q", code, stderr, tt.wantErr)
			}
			if list, _ := h.conns.CloudflareConnections(); len(list)+len(h.cluster.registries) != 0 {
				t.Error("nothing may be stored")
			}
		})
	}
}

// TestConnectionRules covers what may happen to a connection that apps use: a new token, yes;
// another account or removal, no — and the refusal names the apps.
func TestConnectionRules(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.defineRegistry(t, "ghcr", "tobi", "ghcr-secret-token")
	h.defineRegistry(t, "spare", "tobi", "ghcr-secret-token")
	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--registry", "ghcr", "--cloudflare", "tobile")

	stdout := h.mustRun(t, "connection", "list")
	for _, want := range []string{"ghcr", "tobi@ghcr.io", "used by greeter", "spare", "unused", "account acc-1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("list lacks %q:\n%s", want, stdout)
		}
	}

	for _, kind := range []string{"registry ghcr", "cloudflare tobile"} {
		args := append([]string{"connection", "rm"}, strings.Fields(kind)...)
		_, stderr, code := h.run(t, args...)
		if code == 0 || !strings.Contains(stderr, "is used by greeter") {
			t.Errorf("rm %s: exit code %d, stderr %q", kind, code, stderr)
		}
	}
	h.env[envCloudflareToken], h.env[envCloudflareAccount] = "cf-other-token", "acc-2"
	_, stderr, code := h.run(t, "connection", "add", "cloudflare", "tobile")
	if code == 0 || !strings.Contains(stderr, "the tunnels of greeter live there") {
		t.Errorf("moving a used connection to another account: exit code %d, stderr %q", code, stderr)
	}
	delete(h.env, envCloudflareToken)
	delete(h.env, envCloudflareAccount)

	// A new login for a used connection is fine, and says who pulls with it.
	stdout = h.defineRegistry(t, "ghcr", "tobi", "ghcr-new-token")
	if !strings.Contains(stdout, "greeter pull with it from now on") {
		t.Errorf("stdout:\n%s", stdout)
	}
	h.mustRun(t, "connection", "rm", "registry", "spare")
	if _, ok := h.cluster.registries["spare"]; ok {
		t.Error("an unused connection was not removed")
	}
	if _, _, code := h.run(t, "connection", "rm", "registry", "spare"); code == 0 {
		t.Error("removing a missing connection must fail")
	}
}

func TestAppAddWithCloudflare(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)

	stdout := h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "tobile")
	if !slices.Equal(h.api.created, []string{"acc-1/shelf-dev-greeter"}) {
		t.Errorf("created %v; the tunnel is the app's own, in the connection's account", h.api.created)
	}
	added := h.lastAdded(t)
	if added.Cloudflare != "tobile" || added.TunnelID != "tunnel-1" ||
		!strings.Contains(string(added.TunnelCredentials), "tunnel-1") {
		t.Errorf("the cluster got connection %q, tunnel %q with %s", added.Cloudflare, added.TunnelID, added.TunnelCredentials)
	}
	if added.Domain != "" {
		t.Errorf("domain %q; without --domain the app keeps the cluster's", added.Domain)
	}
	want := []string{"greeter-dev.example.com -> tunnel-1.cfargotunnel.com in zone-example.com"}
	if !slices.Equal(h.api.records, want) {
		t.Errorf("records %v, want %v", h.api.records, want)
	}
	for _, line := range []string{
		"Cloudflare connection: tobile",
		"Cloudflare tunnel shelf-dev-greeter: created",
		"DNS greeter-dev.example.com points at tunnel-1.cfargotunnel.com: created",
	} {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout lacks %q:\n%s", line, stdout)
		}
	}

	// The next deploy keeps the connection, and the tunnel whose credentials the cluster holds.
	stdout = h.mustRun(t, "app", "add", "greeter", greeterArtifact)
	if len(h.api.created) != 1 || len(h.api.deleted) != 0 {
		t.Errorf("created %v, deleted %v; the tunnel is reused", h.api.created, h.api.deleted)
	}
	if added := h.lastAdded(t); added.Cloudflare != "tobile" || added.TunnelID != "tunnel-1" || added.TunnelCredentials != nil {
		t.Errorf("connection %q, tunnel %q with credentials %s; the stored ones are kept",
			added.Cloudflare, added.TunnelID, added.TunnelCredentials)
	}
	if !strings.Contains(stdout, "Cloudflare tunnel shelf-dev-greeter: unchanged") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// TestAppAddPrivateByDefault covers an app without a Cloudflare connection: it stays inside the
// cluster unless it asks for a quick tunnel, which needs neither Cloudflare nor a public domain.
func TestAppAddPrivateByDefault(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.cluster.settings = cluster.Settings{Domain: "dev.local", HostSuffix: "-dev"}

	h.mustRun(t, "app", "add", "greeter", greeterArtifact)
	if added := h.lastAdded(t); added.Quick || added.TunnelID != "" || added.Cloudflare != "" {
		t.Errorf("quick %t, tunnel %q, connection %q; a new app stays inside the cluster",
			added.Quick, added.TunnelID, added.Cloudflare)
	}

	stdout := h.mustRun(t, "app", "add", "notes", greeterArtifact, "--quick")
	if added := h.lastAdded(t); !added.Quick || added.TunnelID != "" {
		t.Errorf("quick %t, tunnel %q; --quick gives the app a quick tunnel", added.Quick, added.TunnelID)
	}
	if !strings.Contains(stdout, "internet: a quick tunnel") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if len(h.api.created)+len(h.api.records)+len(h.api.zoneFor) != 0 {
		t.Error("a quick tunnel needs nothing from the Cloudflare API")
	}

	// The next deploy keeps what the app has.
	h.mustRun(t, "app", "add", "notes", greeterArtifact)
	if added := h.lastAdded(t); !added.Quick {
		t.Error("a deploy without flags keeps the quick tunnel")
	}
	h.mustRun(t, "app", "credentials", "notes", "--private")
	if added := h.lastAdded(t); added.Quick {
		t.Error("--private stops the quick tunnel")
	}
}

func TestAppAddWithADomainOfItsOwn(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.cluster.settings = cluster.Settings{Domain: "dev.local", HostSuffix: "-dev"}

	_, stderr, code := h.run(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "tobile")
	if code == 0 || !strings.Contains(stderr, "dev.local, which is not a public domain") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
	if len(h.api.created) != 0 || len(h.cluster.added) != 0 {
		t.Fatal("nothing may be created for a name that cannot exist")
	}

	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "tobile", "--domain", "shop.ch")
	if added := h.lastAdded(t); added.Domain != "shop.ch" {
		t.Errorf("domain %q", added.Domain)
	}
	want := []string{"greeter-dev.shop.ch -> tunnel-1.cfargotunnel.com in zone-shop.ch"}
	if !slices.Equal(h.api.records, want) {
		t.Errorf("records %v, want %v", h.api.records, want)
	}
}

func TestAppCredentialsMovesTheApp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		wantCreated []string
		wantDeleted []string
		wantRecords []string
		wantGone    []string
		wantTunnel  string
		wantQuick   bool
	}{
		{
			name:        "another domain in the same account",
			args:        []string{"--domain", "other.ch"},
			wantRecords: []string{"greeter-dev.other.ch -> tunnel-1.cfargotunnel.com in zone-other.ch"},
			wantGone:    []string{"greeter-dev.example.com in zone-example.com"},
			wantTunnel:  "tunnel-1",
		},
		{
			name:        "a connection in another account",
			args:        []string{"--cloudflare", "club", "--domain", "other.ch"},
			wantCreated: []string{"acc-2/shelf-dev-greeter"},
			wantDeleted: []string{"tunnel-1"},
			wantRecords: []string{"greeter-dev.other.ch -> tunnel-2.cfargotunnel.com in zone-other.ch"},
			wantGone:    []string{"greeter-dev.example.com in zone-example.com"},
			wantTunnel:  "tunnel-2",
		},
		{
			name:        "to a quick tunnel",
			args:        []string{"--quick"},
			wantDeleted: []string{"tunnel-1"},
			wantGone:    []string{"greeter-dev.example.com in zone-example.com"},
			wantQuick:   true,
		},
		{
			name:        "off the internet",
			args:        []string{"--private"},
			wantDeleted: []string{"tunnel-1"},
			wantGone:    []string{"greeter-dev.example.com in zone-example.com"},
		},
		{
			name:        "without a connection, which is off the internet",
			args:        []string{"--no-cloudflare"},
			wantDeleted: []string{"tunnel-1"},
			wantGone:    []string{"greeter-dev.example.com in zone-example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := cloudflareHarness(t)
			h.defineCloudflare(t, "club", "cf-other-token", "acc-2")
			h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "tobile")
			h.api.created, h.api.records = nil, nil

			stdout := h.mustRun(t, append([]string{"app", "credentials", "greeter"}, tt.args...)...)
			if !slices.Equal(h.api.created, tt.wantCreated) {
				t.Errorf("created %v, want %v", h.api.created, tt.wantCreated)
			}
			if !slices.Equal(h.api.deleted, tt.wantDeleted) {
				t.Errorf("deleted tunnels %v, want %v", h.api.deleted, tt.wantDeleted)
			}
			if !slices.Equal(h.api.records, tt.wantRecords) {
				t.Errorf("records %v, want %v", h.api.records, tt.wantRecords)
			}
			if !slices.Equal(h.api.deletedRecords, tt.wantGone) {
				t.Errorf("deleted records %v, want %v", h.api.deletedRecords, tt.wantGone)
			}
			added := h.lastAdded(t)
			if added.TunnelID != tt.wantTunnel || added.Quick != tt.wantQuick || added.Artifact.String() != greeterArtifact {
				t.Errorf("the cluster got tunnel %q, quick %t and artifact %s", added.TunnelID, added.Quick, added.Artifact)
			}
			if stored, _ := h.conns.Cloudflare("tobile"); stored == nil {
				t.Error("a connection belongs to no app; it stays when an app leaves it")
			}
			if !strings.Contains(stdout, "Changing the credentials of app greeter") {
				t.Errorf("stdout:\n%s", stdout)
			}
		})
	}
}

// TestAppAddReplacesATunnelWithoutCredentials covers a tunnel of the app's name whose secret the
// cluster does not hold, which happens after the cluster was rebuilt. Cloudflare hands the secret
// out only once, so the tunnel is useless and is replaced.
func TestAppAddReplacesATunnelWithoutCredentials(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.api.tunnels = map[string]*cloudflare.Tunnel{
		"acc-1/shelf-dev-greeter": {ID: "old-tunnel", Name: "shelf-dev-greeter"},
	}
	stdout := h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "tobile")
	if !slices.Equal(h.api.deleted, []string{"old-tunnel"}) || len(h.api.created) != 1 {
		t.Errorf("deleted %v, created %v", h.api.deleted, h.api.created)
	}
	if !strings.Contains(stdout, "Cloudflare tunnel shelf-dev-greeter: replaced") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// TestConnectionNotOnThisMachine covers an exposed app changed from a machine that does not hold
// its Cloudflare connection: nothing about the exposure may change, and what stays behind is named.
func TestConnectionNotOnThisMachine(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "tobile")
	if _, err := h.conns.DeleteCloudflare("tobile"); err != nil {
		t.Fatal(err)
	}
	h.api.created, h.api.records = nil, nil

	stdout := h.mustRun(t, "app", "add", "greeter", greeterArtifact)
	if added := h.lastAdded(t); added.TunnelID != "tunnel-1" || added.Cloudflare != "tobile" {
		t.Errorf("tunnel %q, connection %q; an exposed app stays exposed", added.TunnelID, added.Cloudflare)
	}
	if len(h.api.created)+len(h.api.deleted)+len(h.api.records)+len(h.api.deletedRecords) != 0 {
		t.Errorf("Cloudflare was changed without the connection: %+v", h.api)
	}
	if !strings.Contains(stdout, "shelf connection add cloudflare tobile") {
		t.Errorf("stdout lacks the way back:\n%s", stdout)
	}
	listed := h.mustRun(t, "connection", "list")
	if !strings.Contains(listed, "tobile") || !strings.Contains(listed, "not on this machine") {
		t.Errorf("list:\n%s", listed)
	}

	stdout = h.mustRun(t, "app", "rm", "greeter", "--yes")
	for _, want := range []string{"the record of\ngreeter-dev.example.com", "tunnel\nshelf-dev-greeter"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if len(h.api.deleted) != 0 {
		t.Errorf("deleted %v", h.api.deleted)
	}
}

func TestAppRmWithdrawsTheExposure(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "tobile")

	stdout := h.mustRun(t, "app", "rm", "greeter", "--yes")
	if !slices.Equal(h.api.deletedRecords, []string{"greeter-dev.example.com in zone-example.com"}) {
		t.Errorf("deleted records %v", h.api.deletedRecords)
	}
	if !slices.Equal(h.api.deleted, []string{"tunnel-1"}) {
		t.Errorf("deleted tunnels %v", h.api.deleted)
	}
	for _, want := range []string{"DNS greeter-dev.example.com: deleted", "Cloudflare tunnel shelf-dev-greeter: deleted"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if stored, _ := h.conns.Cloudflare("tobile"); stored == nil {
		t.Error("the connection went with the app")
	}
	if _, err := os.Stat(h.conns.CloudflarePath("tobile")); err != nil {
		t.Error(err)
	}
}

func TestAccessErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "unknown registry connection",
			args:    []string{"app", "add", "greeter", greeterArtifact, "--registry", "nope"},
			wantErr: "there is no registry connection nope; define it with `shelf connection add registry nope`",
		},
		{
			name:    "unknown Cloudflare connection",
			args:    []string{"app", "add", "greeter", greeterArtifact, "--cloudflare", "nope"},
			wantErr: "this machine holds no Cloudflare connection nope",
		},
		{
			name:    "invalid domain",
			args:    []string{"app", "add", "greeter", greeterArtifact, "--domain", "Not A Domain"},
			wantErr: "the domain must be a DNS name",
		},
		{
			name:    "nothing to change",
			args:    []string{"app", "credentials", "greeter"},
			wantErr: "nothing to change",
		},
		{
			name:    "a connection and a quick tunnel",
			args:    []string{"app", "credentials", "greeter", "--cloudflare", "tobile", "--no-cloudflare"},
			wantErr: "or not at all; choose one",
		},
		{
			name:    "a quick tunnel and private",
			args:    []string{"app", "credentials", "greeter", "--quick", "--private"},
			wantErr: "or not at all; choose one",
		},
		{
			name:    "a quick tunnel with a connection",
			args:    []string{"app", "add", "greeter", greeterArtifact, "--cloudflare", "tobile", "--quick"},
			wantErr: "or not at all; choose one",
		},
		{
			name:    "unknown app",
			args:    []string{"app", "credentials", "nope", "--domain", "shop.ch"},
			wantErr: "app nope does not exist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := cloudflareHarness(t)
			_, stderr, code := h.run(t, tt.args...)
			if code == 0 || !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("exit code %d, stderr %q, want %q", code, stderr, tt.wantErr)
			}
			if len(h.cluster.added) != 0 || len(h.api.created) != 0 {
				t.Error("nothing may change")
			}
			if _, err := os.Stat(h.backup.Path("greeter")); !errors.Is(err, os.ErrNotExist) {
				t.Error("nothing may be written before the connections are known")
			}
		})
	}
}

// TestAppRegistryConnection covers the registry connection of an app: it is what the cluster
// pulls with and what shelf reads the artifact with, it stays with the next deploy, a new login
// for it is used from then on, and it can be dropped.
func TestAppRegistryConnection(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.app = appWithSecrets("hello")
	const private = "oci://ghcr.io/tweinmann/hello:main"
	h.defineRegistry(t, "ghcr", "tobi", "ghcr-secret-token")

	stdout := h.mustRun(t, "app", "add", "hello", private, "--registry", "ghcr")
	if got := h.lastAdded(t).Registry; got != "ghcr" {
		t.Fatalf("the cluster got registry connection %q", got)
	}
	if !strings.Contains(stdout, "registry connection: ghcr") {
		t.Errorf("stdout:\n%s", stdout)
	}
	offered := func() (string, string) {
		t.Helper()
		if h.pull == nil {
			t.Fatal("the connection's login was not used to read the artifact")
		}
		cfg, err := h.pull.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Username, cfg.Password
	}
	if user, token := offered(); user != "tobi" || token != "ghcr-secret-token" {
		t.Errorf("offered %s with %s", user, token)
	}

	// A new login for the connection is what the next read uses; the app keeps the connection.
	h.defineRegistry(t, "ghcr", "tobi", "ghcr-new-token")
	h.mustRun(t, "app", "add", "hello", private)
	if got := h.lastAdded(t).Registry; got != "ghcr" {
		t.Errorf("the connection was not kept: %q", got)
	}
	if _, token := offered(); token != "ghcr-new-token" {
		t.Error("the new login was not used")
	}

	// A login is only offered to the registry it is for.
	h.mustRun(t, "app", "add", "hello", helloArtifact, "--insecure-registry")
	if h.pull != nil {
		t.Error("the ghcr.io login was offered to another registry")
	}

	h.mustRun(t, "app", "credentials", "hello", "--no-registry")
	if got := h.lastAdded(t).Registry; got != "" {
		t.Errorf("the connection was not dropped: %q", got)
	}
}
