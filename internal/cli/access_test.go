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

// cloudflareHarness is a cluster with a public domain and a Cloudflare token at hand.
func cloudflareHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t).public()
	h.app = appWithSecrets("greeter")
	h.cluster.found = true
	h.env[envCloudflareToken] = "cf-secret-token"
	return h
}

// mustRun runs a command that has to succeed and returns what it printed. It fails the test if a
// token shows up anywhere in the output.
func (h *harness) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, code := h.run(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit code %d: %s", args, code, stderr)
	}
	for _, token := range []string{"cf-secret-token", "cf-other-token", "ghcr-secret-token"} {
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

func TestAppAddWithCloudflare(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)

	stdout := h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare")
	if !slices.Equal(h.api.created, []string{"acc-1/shelf-dev-greeter"}) {
		t.Errorf("created %v; the tunnel is the app's own, in the token's account", h.api.created)
	}
	added := h.lastAdded(t)
	if added.TunnelID != "tunnel-1" || !strings.Contains(string(added.TunnelCredentials), "tunnel-1") {
		t.Errorf("the cluster got tunnel %q with %s", added.TunnelID, added.TunnelCredentials)
	}
	if added.Domain != "" {
		t.Errorf("domain %q; without --domain the app keeps the cluster's", added.Domain)
	}
	want := []string{"greeter-dev.example.com -> tunnel-1.cfargotunnel.com in zone-example.com"}
	if !slices.Equal(h.api.records, want) {
		t.Errorf("records %v, want %v", h.api.records, want)
	}
	for _, line := range []string{
		"Cloudflare tunnel shelf-dev-greeter: created",
		"DNS greeter-dev.example.com points at tunnel-1.cfargotunnel.com: created",
		"Cloudflare access: " + h.access.Path("greeter"),
	} {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout lacks %q:\n%s", line, stdout)
		}
	}
	stored, err := h.access.Cloudflare("greeter")
	if err != nil || stored == nil || *stored != (hostcfg.Cloudflare{Token: "cf-secret-token", Account: "acc-1"}) {
		t.Fatalf("stored access %+v, %v", stored, err)
	}

	// The next deploy needs no token in the environment: the access is on this machine, and the
	// tunnel whose credentials the cluster holds is kept.
	delete(h.env, envCloudflareToken)
	stdout = h.mustRun(t, "app", "add", "greeter", greeterArtifact)
	if len(h.api.created) != 1 || len(h.api.deleted) != 0 {
		t.Errorf("created %v, deleted %v; the tunnel is reused", h.api.created, h.api.deleted)
	}
	if added := h.lastAdded(t); added.TunnelID != "tunnel-1" || added.TunnelCredentials != nil {
		t.Errorf("tunnel %q with credentials %s; the stored ones are kept", added.TunnelID, added.TunnelCredentials)
	}
	if !strings.Contains(stdout, "Cloudflare tunnel shelf-dev-greeter: unchanged") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestAppAddWithADomainOfItsOwn(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.cluster.settings = cluster.Settings{Domain: "dev.local", HostSuffix: "-dev"}

	_, stderr, code := h.run(t, "app", "add", "greeter", greeterArtifact, "--cloudflare")
	if code == 0 || !strings.Contains(stderr, "dev.local, which is not a public domain") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
	if len(h.api.created) != 0 || len(h.cluster.added) != 0 {
		t.Fatal("nothing may be created for a name that cannot exist")
	}

	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "--domain", "shop.ch")
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
		env         map[string]string
		wantCreated []string
		wantDeleted []string
		wantRecords []string
		wantGone    []string
		wantTunnel  string
	}{
		{
			name:        "another domain in the same account",
			args:        []string{"--domain", "other.ch"},
			wantRecords: []string{"greeter-dev.other.ch -> tunnel-1.cfargotunnel.com in zone-other.ch"},
			wantGone:    []string{"greeter-dev.example.com in zone-example.com"},
			wantTunnel:  "tunnel-1",
		},
		{
			name:        "another account",
			args:        []string{"--cloudflare", "--domain", "other.ch"},
			env:         map[string]string{envCloudflareToken: "cf-other-token", envCloudflareAccount: "acc-2"},
			wantCreated: []string{"acc-2/shelf-dev-greeter"},
			wantDeleted: []string{"tunnel-1"},
			wantRecords: []string{"greeter-dev.other.ch -> tunnel-2.cfargotunnel.com in zone-other.ch"},
			wantGone:    []string{"greeter-dev.example.com in zone-example.com"},
			wantTunnel:  "tunnel-2",
		},
		{
			name:        "off the internet",
			args:        []string{"--no-cloudflare"},
			wantDeleted: []string{"tunnel-1"},
			wantGone:    []string{"greeter-dev.example.com in zone-example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := cloudflareHarness(t)
			h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare")
			h.api.created, h.api.records = nil, nil
			delete(h.env, envCloudflareToken)
			for k, v := range tt.env {
				h.env[k] = v
			}

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
			if added.TunnelID != tt.wantTunnel || added.Artifact.String() != greeterArtifact {
				t.Errorf("the cluster got tunnel %q and artifact %s", added.TunnelID, added.Artifact)
			}
			stored, err := h.access.Cloudflare("greeter")
			if err != nil {
				t.Fatal(err)
			}
			if (tt.wantTunnel == "") != (stored == nil) {
				t.Errorf("stored access %+v after %s", stored, tt.name)
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
	stdout := h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare")
	if !slices.Equal(h.api.deleted, []string{"old-tunnel"}) || len(h.api.created) != 1 {
		t.Errorf("deleted %v, created %v", h.api.deleted, h.api.created)
	}
	if !strings.Contains(stdout, "Cloudflare tunnel shelf-dev-greeter: replaced") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// TestAccessNotOnThisMachine covers an exposed app changed from a machine that does not hold its
// Cloudflare access: nothing about the exposure may change, and what stays behind is named.
func TestAccessNotOnThisMachine(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare")
	if err := h.access.DeleteCloudflare("greeter"); err != nil {
		t.Fatal(err)
	}
	h.api.created, h.api.records = nil, nil

	stdout := h.mustRun(t, "app", "add", "greeter", greeterArtifact)
	if added := h.lastAdded(t); added.TunnelID != "tunnel-1" {
		t.Errorf("tunnel %q; an exposed app stays exposed", added.TunnelID)
	}
	if len(h.api.created)+len(h.api.deleted)+len(h.api.records)+len(h.api.deletedRecords) != 0 {
		t.Errorf("Cloudflare was changed without the access: %+v", h.api)
	}
	if !strings.Contains(stdout, "does not hold its Cloudflare access") {
		t.Errorf("stdout lacks the warning:\n%s", stdout)
	}

	stdout = h.mustRun(t, "app", "rm", "greeter", "--yes")
	for _, want := range []string{"the record of greeter-dev.example.com", "tunnel shelf-dev-greeter"} {
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
	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare")

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
	if _, err := os.Stat(h.access.Path("greeter")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the access of a removed app is kept: %v", err)
	}
}

func TestAccessErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		setup   func(*harness)
		wantErr string
	}{
		{
			name:    "no token",
			args:    []string{"app", "add", "greeter", greeterArtifact, "--cloudflare"},
			setup:   func(h *harness) { delete(h.env, envCloudflareToken) },
			wantErr: "--cloudflare needs CF_API_TOKEN",
		},
		{
			name:    "token refused",
			args:    []string{"app", "add", "greeter", greeterArtifact, "--cloudflare"},
			setup:   func(h *harness) { h.api.verifyErr = errors.New("Invalid request headers (code 6003)") },
			wantErr: "the Cloudflare token was refused",
		},
		{
			name:    "several accounts",
			args:    []string{"app", "add", "greeter", greeterArtifact, "--cloudflare"},
			setup:   func(h *harness) { h.api.accounts = []string{"acc-1", "acc-2"} },
			wantErr: "CF_ACCOUNT_ID",
		},
		{
			name:    "no registry token",
			args:    []string{"app", "add", "greeter", greeterArtifact, "--registry-login"},
			setup:   func(h *harness) { h.env[envRegistryUser] = "tobi" },
			wantErr: "--registry-login needs GHCR_USERNAME and GHCR_TOKEN",
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
			name:    "given and removed",
			args:    []string{"app", "credentials", "greeter", "--cloudflare", "--no-cloudflare"},
			wantErr: "given and removed at once",
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
			if tt.setup != nil {
				tt.setup(h)
			}
			_, stderr, code := h.run(t, tt.args...)
			if code == 0 || !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("exit code %d, stderr %q, want %q", code, stderr, tt.wantErr)
			}
			if len(h.cluster.added) != 0 || len(h.api.created) != 0 {
				t.Error("nothing may change")
			}
			if strings.Contains(stderr, "cf-secret-token") {
				t.Error("the token appears in the error")
			}
		})
	}
}

// TestAppRegistryLogin covers the app's own login: it is stored with the app, used to read the
// deploy artifact from here as well, kept by the next deploy, and dropped on request.
func TestAppRegistryLogin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.app = appWithSecrets("hello")
	h.env[envRegistryUser], h.env[envRegistryToken] = "tobi", "ghcr-secret-token"
	const private = "oci://ghcr.io/tweinmann/hello:main"

	stdout := h.mustRun(t, "app", "add", "hello", private, "--registry-login")
	login := h.lastAdded(t).Registry
	if login == nil || login.Username != "tobi" || login.Token != "ghcr-secret-token" {
		t.Fatalf("the cluster got login %+v", login)
	}
	if !strings.Contains(stdout, "registry login: tobi for ghcr.io") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if h.pull == nil {
		t.Fatal("the app's login was not used to read the artifact")
	}
	cfg, err := h.pull.Authorization()
	if err != nil || cfg.Username != "tobi" || cfg.Password != "ghcr-secret-token" {
		t.Errorf("offered %+v, %v", cfg, err)
	}

	// Without the flag, the variables are not read, and the stored login is kept.
	delete(h.env, envRegistryToken)
	h.mustRun(t, "app", "add", "hello", private)
	if login := h.lastAdded(t).Registry; login == nil || login.Username != "tobi" {
		t.Errorf("the login was not kept: %+v", login)
	}

	// A login is only offered to the registry it is for.
	h.mustRun(t, "app", "add", "hello", helloArtifact, "--insecure-registry")
	if h.pull != nil {
		t.Error("the ghcr.io login was offered to another registry")
	}

	h.mustRun(t, "app", "credentials", "hello", "--no-registry-login")
	if login := h.lastAdded(t).Registry; login != nil {
		t.Errorf("the login was not dropped: %+v", login)
	}
}
