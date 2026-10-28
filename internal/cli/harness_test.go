package cli

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
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
	// stored are the secret values of the apps in the cluster, by app.
	stored map[string]map[string]string
	// configs are the apps as they are registered, by name. AddApp writes them, as the real
	// cluster does, so that a second command sees what the first one stored.
	configs map[string]*cluster.AppConfig
	// registries are the registry connections, by name.
	registries map[string]cluster.RegistryAuth
	apps       []string
	states     []cluster.AppState
	components []cluster.Component
	stages     []cluster.Stage
	// rollouts are what AppRollout returns, one per call; the last one is repeated.
	rollouts     []cluster.Rollout
	rolloutCalls int
	// rolloutMissing is how many reads of a rollout find nothing before it appears, as a preview
	// that the platform has not created yet.
	rolloutMissing int
	// verifications are what AppVerification returns, by app or preview namespace; without an
	// entry, the app is verified and its one component scanned.
	verifications map[string]cluster.Verification
	// traefik is the address ForwardTraefik hands out, empty when no Traefik pod is ready.
	traefik string
	found   bool
	// noPlatform is a cluster that init cluster never installed the platform into, oldPlatform
	// one whose platform predates quick tunnels.
	noPlatform  bool
	oldPlatform bool

	installed  []cluster.Options
	added      []cluster.AppOptions
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

func (f *fakeCluster) CheckPlatform(_ context.Context, _ *rest.Config, needs cluster.PlatformNeeds) error {
	switch {
	case f.noPlatform:
		return errors.New("this cluster has no shelf platform; install it with `shelf init cluster`")
	case f.oldPlatform && needs.Previews:
		return errors.New("the platform in this cluster predates previews; install the platform of " +
			"this shelf with `shelf init cluster` first, or leave out --previews")
	case f.oldPlatform && needs.Quick:
		return errors.New("the platform in this cluster does not run quick tunnels yet; " +
			"install the platform of this shelf with `shelf init cluster` first, or add the app without --quick")
	}
	return nil
}

func (f *fakeCluster) AddApp(_ context.Context, _ *rest.Config, o cluster.AppOptions) error {
	f.added = append(f.added, o)
	if f.stored == nil {
		f.stored = map[string]map[string]string{}
	}
	f.stored[o.Name] = o.Secrets
	if f.configs == nil {
		f.configs = map[string]*cluster.AppConfig{}
	}
	config := &cluster.AppConfig{
		Artifact: o.Artifact, Insecure: o.Insecure, Domain: o.Domain, Registry: o.Registry,
		Cloudflare: o.Cloudflare, TunnelID: o.TunnelID, TunnelCredentials: o.TunnelCredentials, Quick: o.Quick,
		Previews: o.Previews,
	}
	if previous := f.configs[o.Name]; previous != nil && o.TunnelCredentials == nil && o.TunnelID != "" {
		config.TunnelCredentials = previous.TunnelCredentials
	}
	f.configs[o.Name] = config
	// The states follow what is registered, as they do in a cluster.
	f.states = slices.DeleteFunc(f.states, func(s cluster.AppState) bool { return s.Name == o.Name })
	f.states = append(f.states, cluster.AppState{
		Name: o.Name, Artifact: o.Artifact, Domain: o.Domain, Tunnel: o.TunnelID, Quick: o.Quick,
		Registry: o.Registry, Cloudflare: o.Cloudflare, PreviewLimit: o.Previews,
	})
	return nil
}

func (f *fakeCluster) RemoveApp(_ context.Context, _ *rest.Config, app string, _ time.Duration,
	_ progress.Reporter) (bool, error) {
	f.removed = append(f.removed, app)
	delete(f.configs, app)
	f.states = slices.DeleteFunc(f.states, func(s cluster.AppState) bool { return s.Name == app })
	return f.found, nil
}

func (f *fakeCluster) Redeploy(_ context.Context, _ *rest.Config, app string, _ time.Duration,
	_ progress.Reporter) error {
	f.redeployed = append(f.redeployed, app)
	return nil
}

func (f *fakeCluster) AppSecrets(_ context.Context, _ *rest.Config, app string) (map[string]string, error) {
	return f.stored[app], nil
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

func (f *fakeCluster) AppRollout(_ context.Context, _ *rest.Config, app string) (cluster.Rollout, error) {
	if f.rolloutMissing > 0 {
		f.rolloutMissing--
		return cluster.Rollout{}, &cluster.NotFoundError{Name: app}
	}
	if !slices.ContainsFunc(f.states, func(s cluster.AppState) bool { return s.Name == app }) &&
		!strings.Contains(app, "--") {
		return cluster.Rollout{}, &cluster.NotFoundError{Name: app}
	}
	r := f.rollouts[min(f.rolloutCalls, len(f.rollouts)-1)]
	f.rolloutCalls++
	return r, nil
}

func (f *fakeCluster) AppVerification(_ context.Context, _ *rest.Config, name string) (cluster.Verification, error) {
	if v, ok := f.verifications[name]; ok {
		return v, nil
	}
	return cluster.Verification{
		Verified: true,
		Source:   "https://github.com/acme/hello",
		Commit:   "42a964eb0b4bd7248305d5f22233d6dbabcd56f3",
		Scan: &cluster.Scan{Scanner: "trivy 0.75.0", ScannedAt: "2026-10-28T12:00:00Z", Images: []cluster.ScannedImage{{
			Component: "web", Image: "traefik/whoami:v1.11.0@sha256:2006", Digest: "sha256:2006",
			Scanned: true, Critical: 1, High: 26,
		}}},
	}, nil
}

func (f *fakeCluster) ForwardTraefik(context.Context, *rest.Config) (*cluster.Forward, error) {
	if f.traefik == "" {
		return nil, errors.New("no Traefik pod is ready")
	}
	return &cluster.Forward{Addr: f.traefik, Done: make(chan struct{})}, nil
}

func (f *fakeCluster) Settings(context.Context, *rest.Config) (cluster.Settings, error) {
	return f.settings, f.settingsErr
}

func (f *fakeCluster) AppConfig(_ context.Context, _ *rest.Config, app string) (*cluster.AppConfig, error) {
	return f.configs[app], nil
}

func (f *fakeCluster) RegistryConnections(context.Context, *rest.Config) ([]cluster.RegistryConnection, error) {
	var out []cluster.RegistryConnection
	for _, name := range slices.Sorted(maps.Keys(f.registries)) {
		out = append(out, cluster.RegistryConnection{Name: name, Username: f.registries[name].Username})
	}
	return out, nil
}

func (f *fakeCluster) RegistryConnection(_ context.Context, _ *rest.Config, name string) (*cluster.RegistryAuth, error) {
	auth, ok := f.registries[name]
	if !ok {
		return nil, nil
	}
	return &auth, nil
}

func (f *fakeCluster) SaveRegistryConnection(_ context.Context, _ *rest.Config, name string,
	auth cluster.RegistryAuth, rep progress.Reporter) error {
	if f.registries == nil {
		f.registries = map[string]cluster.RegistryAuth{}
	}
	f.registries[name] = auth
	rep.Report(progress.Applied("Secret shelf-system/"+cluster.RegistryConnectionSecretName(name), "configured",
		"for "+auth.Username+"@ghcr.io"))
	return nil
}

func (f *fakeCluster) DeleteRegistryConnection(_ context.Context, _ *rest.Config, name string,
	_ progress.Reporter) (bool, error) {
	_, ok := f.registries[name]
	delete(f.registries, name)
	return ok, nil
}

// harness runs commands against fakes. Everything a command reads from the outside world is a
// field here, so a test changes it without touching the process: no environment variables, no
// package-level variables, and therefore no reason not to run in parallel.
type harness struct {
	kubeconfig string
	env        map[string]string
	backup     secrets.Backup
	conns      hostcfg.Connections
	version    string

	cluster *fakeCluster
	api     *fakeCloudflare
	gh      *fakeGitHub

	// app is what the registry serves as the deploy artifact.
	app      *schema.App
	fetchErr error
	// fetched records the reference and whether TLS was skipped.
	fetched  string
	insecure bool
	pull     authn.Authenticator
	// browser is what --browser opens an address with.
	browser func(url string) error
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
		conns:      hostcfg.Connections{Dir: filepath.Join(home, hostcfg.ConnectionsDir)},
		version:    "v0.0.0-test",
		cluster:    &fakeCluster{},
		api:        &fakeCloudflare{accounts: []string{"acc-1"}},
		gh:         &fakeGitHub{},
	}
}

// public makes the cluster one that was installed, with the host suffix -dev; its apps can be
// exposed under a domain of their own.
func (h *harness) public() *harness {
	h.cluster.settings = cluster.Settings{HostSuffix: "-dev",
		Chart:  cluster.Artifact{URL: "oci://ghcr.io/tweinmann/shelf/charts/shelf-app", Tag: "0.5.0"},
		Signer: cluster.DefaultSigner()}
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
		OpenBrowser: func(url string) error {
			if h.browser == nil {
				return errors.New("no browser in tests")
			}
			return h.browser(url)
		},
		NewOps: func(target ops.Target, env ops.Env) *ops.Ops {
			return &ops.Ops{
				Target:        target,
				Env:           env,
				Cluster:       h.cluster,
				Fetch:         h.fetch,
				NewCloudflare: func(string) ops.CloudflareAPI { return h.api },
				NewGitHub: func(token string) ops.GitHubAPI {
					h.gh.token = token
					return h.gh
				},
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
	return h.runContext(t, context.Background(), stdin, args...)
}

// runContext runs a command until it ends or ctx is cancelled, as Ctrl-C does.
func (h *harness) runContext(t *testing.T, ctx context.Context, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := New(h.options())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append(args, "--kubeconfig", h.kubeconfig))
	code = Execute(ctx, cmd)
	return out.String(), errOut.String(), code
}

// appWithSecrets is the app a deploy artifact would carry; name is its package name.
func appWithSecrets(name string, secretNames ...string) *schema.App {
	app := &schema.App{APIVersion: schema.APIVersion, Name: name, Secrets: map[string]*schema.Secret{}}
	for _, s := range secretNames {
		app.Secrets[s] = &schema.Secret{Generate: true}
	}
	return app
}
