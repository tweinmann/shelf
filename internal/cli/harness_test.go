package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
	"github.com/tweinmann/shelf/internal/schema"
	"github.com/tweinmann/shelf/internal/secrets"
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

// fakeCluster records what the commands ask of a cluster, without an API server.
type fakeCluster struct {
	settings    cluster.Settings
	settingsErr error
	// stored are the secret values of the app in the cluster.
	stored      map[string]string
	tunnelCreds []byte
	apps        []string
	states      []cluster.AppState
	components  []cluster.Component
	stages      []cluster.Stage
	found       bool

	installed  []cluster.Options
	added      []cluster.AppOptions
	exposed    []cluster.ExposeOptions
	removed    []string
	redeployed []string
	// host is the API server the operation was pointed at.
	host string
}

func (f *fakeCluster) Install(_ context.Context, cfg *rest.Config, o cluster.Options) error {
	f.installed = append(f.installed, o)
	f.host = cfg.Host
	return nil
}

func (f *fakeCluster) AddApp(_ context.Context, _ *rest.Config, o cluster.AppOptions) error {
	f.added = append(f.added, o)
	f.stored = o.Secrets
	return nil
}

func (f *fakeCluster) RemoveApp(_ context.Context, _ *rest.Config, app string, _ time.Duration,
	_ progress.Reporter) (bool, error) {
	f.removed = append(f.removed, app)
	return f.found, nil
}

func (f *fakeCluster) Redeploy(_ context.Context, _ *rest.Config, app string, _ time.Duration,
	_ progress.Reporter) error {
	f.redeployed = append(f.redeployed, app)
	return nil
}

func (f *fakeCluster) Expose(_ context.Context, _ *rest.Config, o cluster.ExposeOptions) error {
	f.exposed = append(f.exposed, o)
	return nil
}

func (f *fakeCluster) AppSecrets(_ context.Context, _ *rest.Config, _ string) (map[string]string, error) {
	return f.stored, nil
}

func (f *fakeCluster) AppNames(context.Context, *rest.Config) ([]string, error) { return f.apps, nil }

func (f *fakeCluster) AppStates(context.Context, *rest.Config) ([]cluster.AppState, error) {
	return f.states, nil
}

func (f *fakeCluster) AppComponents(context.Context, *rest.Config, string) ([]cluster.Component, error) {
	return f.components, nil
}

func (f *fakeCluster) AppDiagnosis(_ context.Context, _ *rest.Config, app string) (cluster.Diagnosis, error) {
	return cluster.Diagnosis{Name: app, Stages: f.stages}, nil
}

func (f *fakeCluster) Settings(context.Context, *rest.Config) (cluster.Settings, error) {
	return f.settings, f.settingsErr
}

func (f *fakeCluster) TunnelCredentials(context.Context, *rest.Config) ([]byte, error) {
	return f.tunnelCreds, nil
}

// harness runs commands against fakes. Everything a command reads from the outside world is a
// field here, so a test changes it without touching the process: no environment variables, no
// package-level variables, and therefore no reason not to run in parallel.
type harness struct {
	kubeconfig string
	env        map[string]string
	backup     secrets.Backup
	version    string

	cluster *fakeCluster
	api     *fakeCloudflare

	// app is what the registry serves as the deploy artifact.
	app      *schema.App
	fetchErr error
	// fetched records the reference and whether TLS was skipped.
	fetched  string
	insecure bool
	pull     authn.Authenticator
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	kubeconfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kubeconfig, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	return &harness{
		kubeconfig: kubeconfig,
		env:        map[string]string{"SHELF_HOME": home},
		backup:     secrets.Backup{Dir: filepath.Join(home, "apps")},
		version:    "v0.0.0-test",
		cluster:    &fakeCluster{},
		api:        &fakeCloudflare{accounts: []string{"acc-1"}},
	}
}

// exposed makes the cluster one whose apps are reachable from the internet.
func (h *harness) exposed() *harness {
	h.cluster.settings = cluster.Settings{
		Domain: "example.com", HostSuffix: "-dev", TunnelTarget: "t-1.cfargotunnel.com",
	}
	return h
}

func (h *harness) fetch(_ context.Context, ref string, auth authn.Authenticator, insecure bool) (*schema.App, error) {
	h.fetched, h.insecure, h.pull = ref, insecure, auth
	return h.app, h.fetchErr
}

func (h *harness) options() Options {
	return Options{
		Images:  images,
		Version: h.version,
		Getenv:  func(key string) string { return h.env[key] },
		NewOps: func(target ops.Target, env ops.Env) *ops.Ops {
			return &ops.Ops{
				Target:        target,
				Env:           env,
				Cluster:       h.cluster,
				Fetch:         h.fetch,
				NewCloudflare: func(string) ops.CloudflareAPI { return h.api },
			}
		},
	}
}

// run executes a command with the harness's kubeconfig appended.
func (h *harness) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return h.runWithInput(t, "", args...)
}

func (h *harness) runWithInput(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := New(h.options())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append(args, "--kubeconfig", h.kubeconfig))
	code = Execute(context.Background(), cmd)
	return out.String(), errOut.String(), code
}

// appWithSecrets is the app a deploy artifact would carry.
func appWithSecrets(name string, secretNames ...string) *schema.App {
	app := &schema.App{APIVersion: schema.APIVersion, Name: name, Secrets: map[string]*schema.Secret{}}
	for _, s := range secretNames {
		app.Secrets[s] = &schema.Secret{Generate: true}
	}
	return app
}
