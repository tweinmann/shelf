package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/cloudflare"
	"github.com/tweinmann/shelf/internal/cluster"
)

// fakeCloudflare answers like the Cloudflare API without talking to it.
type fakeCloudflare struct {
	accounts  []string
	existing  *cloudflare.Tunnel
	created   []string
	deleted   []string
	accountID string
}

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

// exposeEnv installs fakes for the cluster and Cloudflare and returns them.
type exposeEnv struct {
	api         *fakeCloudflare
	settings    cluster.Settings
	stored      []byte
	settingsErr error
	calls       []cluster.ExposeOptions
	kubeconfig  string
}

func newExposeEnv(t *testing.T) *exposeEnv {
	t.Helper()
	kubeconfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kubeconfig, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	env := &exposeEnv{
		api:        &fakeCloudflare{accounts: []string{"acc-1"}},
		settings:   cluster.Settings{Domain: "example.com", HostSuffix: "-dev"},
		kubeconfig: kubeconfig,
	}
	oldSettings, oldCreds, oldExpose, oldNew := clusterSettings, tunnelCredentials, exposeCluster, newCloudflare
	t.Cleanup(func() {
		clusterSettings, tunnelCredentials, exposeCluster, newCloudflare = oldSettings, oldCreds, oldExpose, oldNew
	})
	clusterSettings = func(context.Context, *rest.Config) (cluster.Settings, error) {
		return env.settings, env.settingsErr
	}
	tunnelCredentials = func(context.Context, *rest.Config) ([]byte, error) { return env.stored, nil }
	exposeCluster = func(_ context.Context, _ *rest.Config, o cluster.ExposeOptions) error {
		env.calls = append(env.calls, o)
		return nil
	}
	newCloudflare = func(string) cloudflareAPI { return env.api }
	return env
}

func (e *exposeEnv) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return run(t, append([]string{"init", "expose", "--kubeconfig", e.kubeconfig}, args...)...)
}

func TestInitExposeCreatesTunnel(t *testing.T) {
	env := newExposeEnv(t)
	t.Setenv(envCloudflareToken, "cf-secret-token")

	stdout, stderr, code := env.run(t, "--yes")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "cf-secret-token") {
		t.Fatal("the token appears in the output")
	}
	if len(env.api.created) != 1 || env.api.created[0] != "shelf-dev" {
		t.Errorf("created %v; the tunnel is named after the host suffix", env.api.created)
	}
	if len(env.calls) != 1 {
		t.Fatalf("expose called %d times", len(env.calls))
	}
	got := env.calls[0]
	if got.TunnelID != "new-tunnel" || got.Domain != "example.com" || got.Owner != "shelf-dev" ||
		got.APIToken != "cf-secret-token" || string(got.Credentials) != `{"TunnelID":"new-tunnel"}` {
		t.Errorf("options %+v", got)
	}
	for _, want := range []string{"hosts     <app>-dev.example.com", "tunnel    shelf-dev",
		"dns owner shelf-dev", "target    new-tunnel.cfargotunnel.com"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestInitExposeReusesTunnel(t *testing.T) {
	env := newExposeEnv(t)
	env.api.existing = &cloudflare.Tunnel{ID: "old-tunnel", Name: "shelf-dev"}
	env.stored = []byte(`{"TunnelID":"old-tunnel","TunnelSecret":"x"}`)
	t.Setenv(envCloudflareToken, "cf-secret-token")

	_, stderr, code := env.run(t, "--yes")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if len(env.api.created) != 0 || len(env.api.deleted) != 0 {
		t.Errorf("created %v, deleted %v; an existing tunnel with credentials is reused",
			env.api.created, env.api.deleted)
	}
	if got := env.calls[0]; got.TunnelID != "old-tunnel" || got.Credentials != nil {
		t.Errorf("options %+v; the stored credentials must be kept", got)
	}
}

func TestInitExposeReplacesTunnelWithoutCredentials(t *testing.T) {
	env := newExposeEnv(t)
	env.api.existing = &cloudflare.Tunnel{ID: "old-tunnel", Name: "shelf-dev"}
	t.Setenv(envCloudflareToken, "cf-secret-token")

	stdout, _, code := env.run(t, "--yes")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if len(env.api.deleted) != 1 || env.api.deleted[0] != "old-tunnel" || len(env.api.created) != 1 {
		t.Errorf("deleted %v, created %v", env.api.deleted, env.api.created)
	}
	if !strings.Contains(stdout, "has to be replaced") {
		t.Errorf("stdout does not explain the replacement:\n%s", stdout)
	}
}

func TestInitExposeDeclined(t *testing.T) {
	env := newExposeEnv(t)
	env.api.existing = &cloudflare.Tunnel{ID: "old-tunnel", Name: "shelf-dev"}
	t.Setenv(envCloudflareToken, "cf-secret-token")

	cmd := New(images)
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetArgs([]string{"init", "expose", "--kubeconfig", env.kubeconfig})
	if code := Execute(context.Background(), cmd); code != 1 {
		t.Fatalf("exit code %d", code)
	}
	if len(env.api.deleted) != 0 || len(env.calls) != 0 {
		t.Error("nothing may change when the replacement is declined")
	}
	if !strings.Contains(errOut.String(), "aborted") {
		t.Errorf("stderr %q", errOut.String())
	}
}

func TestInitExposeErrors(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		setup   func(*exposeEnv)
		wantErr string
	}{
		{name: "no token", wantErr: "CF_API_TOKEN is not set"},
		{
			name:    "cluster without domain",
			token:   "cf-secret-token",
			setup:   func(e *exposeEnv) { e.settings = cluster.Settings{} },
			wantErr: "shelf init cluster",
		},
		{
			name:    "several accounts",
			token:   "cf-secret-token",
			setup:   func(e *exposeEnv) { e.api.accounts = []string{"acc-1", "acc-2"} },
			wantErr: "CF_ACCOUNT_ID",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newExposeEnv(t)
			if tt.setup != nil {
				tt.setup(env)
			}
			t.Setenv(envCloudflareToken, tt.token)
			_, stderr, code := env.run(t, "--yes")
			if code == 0 || !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("exit code %d, stderr %q, want %q", code, stderr, tt.wantErr)
			}
			if len(env.calls) != 0 {
				t.Error("nothing may be exposed")
			}
		})
	}
}

func TestInitExposeAccountFromEnvironment(t *testing.T) {
	env := newExposeEnv(t)
	env.api.accounts = []string{"acc-1", "acc-2"}
	t.Setenv(envCloudflareToken, "cf-secret-token")
	t.Setenv(envCloudflareAccount, "acc-2")

	_, stderr, code := env.run(t, "--yes", "--tunnel", "my-tunnel", "--dns-owner", "my-owner")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if env.api.accountID != "acc-2" {
		t.Errorf("account %q", env.api.accountID)
	}
	if got := env.calls[0]; got.Owner != "my-owner" {
		t.Errorf("owner %q", got.Owner)
	}
	if len(env.api.created) != 1 || env.api.created[0] != "my-tunnel" {
		t.Errorf("created %v", env.api.created)
	}
}
