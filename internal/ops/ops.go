// Package ops holds the operations shelf performs on a cluster: install the platform, expose
// it, add and remove apps. They used to live inside the command line, which made the command
// line the only possible caller. Here they have no opinion about who calls them: what belongs
// to a caller — flags, prompts, rendered output — stays with that caller, what belongs to the
// operator's machine arrives in Env, and what happens during an operation is reported as
// progress events.
//
// The rule that keeps the command line and the admin UI from drifting apart is that both call
// these operations, and neither reimplements one.
package ops

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/deploy"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/progress"
	"github.com/tweinmann/shelf/internal/schema"
	"github.com/tweinmann/shelf/internal/secrets"
)

// Target is the cluster an operation works on.
type Target struct {
	// Context is the name of the kubeconfig context, for showing which cluster this is.
	Context string
	Config  *rest.Config
}

// LoadTarget resolves a kubeconfig file and context into a target. Both may be empty, which
// means the default file and its current context.
func LoadTarget(kubeconfig, context string) (Target, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig
	overrides := &clientcmd.ConfigOverrides{CurrentContext: context}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	raw, err := loader.RawConfig()
	if err != nil {
		return Target{}, fmt.Errorf("reading kubeconfig: %w", err)
	}
	name := raw.CurrentContext
	if context != "" {
		name = context
	}
	cfg, err := loader.ClientConfig()
	if err != nil {
		return Target{}, fmt.Errorf("kubeconfig: %w", err)
	}
	return Target{Context: name, Config: rest.CopyConfig(cfg)}, nil
}

// Env is what the machine shelf runs on supplies: where secrets are backed up, and where the
// Cloudflare connections are kept. Both live under ~/.shelf, for the command line and the admin
// UI alike.
type Env struct {
	// Backup is where the generated secret values of an app are kept outside the cluster.
	Backup secrets.Backup
	// Connections is where the Cloudflare connections are kept. They never go into the cluster.
	Connections hostcfg.Connections
	// Pull are the credentials for reading a deploy artifact from here. Nil takes the login of
	// the app's registry connection, and without one the Docker config.
	Pull authn.Authenticator
}

// Cluster is the part of internal/cluster the operations use. Tests replace it; everything
// behind it needs an API server.
type Cluster interface {
	Install(ctx context.Context, cfg *rest.Config, o cluster.Options) error
	AddApp(ctx context.Context, cfg *rest.Config, o cluster.AppOptions) error
	RemoveApp(ctx context.Context, cfg *rest.Config, app string, timeout time.Duration,
		rep progress.Reporter) (bool, error)
	Redeploy(ctx context.Context, cfg *rest.Config, app string, timeout time.Duration,
		rep progress.Reporter) error
	AppSecrets(ctx context.Context, cfg *rest.Config, app string) (map[string]string, error)
	AppNames(ctx context.Context, cfg *rest.Config) ([]string, error)
	AppStates(ctx context.Context, cfg *rest.Config) ([]cluster.AppState, error)
	AppComponents(ctx context.Context, cfg *rest.Config, app string) ([]cluster.Component, error)
	AppDiagnosis(ctx context.Context, cfg *rest.Config, app string) (cluster.Diagnosis, error)
	Settings(ctx context.Context, cfg *rest.Config) (cluster.Settings, error)
	// AppConfig returns how an app is registered, or nil when there is no such app.
	AppConfig(ctx context.Context, cfg *rest.Config, app string) (*cluster.AppConfig, error)
	RegistryConnections(ctx context.Context, cfg *rest.Config) ([]cluster.RegistryConnection, error)
	// RegistryConnection returns the login of a registry connection, or nil if there is none.
	RegistryConnection(ctx context.Context, cfg *rest.Config, name string) (*cluster.RegistryAuth, error)
	SaveRegistryConnection(ctx context.Context, cfg *rest.Config, name string, auth cluster.RegistryAuth,
		rep progress.Reporter) error
	DeleteRegistryConnection(ctx context.Context, cfg *rest.Config, name string, rep progress.Reporter) (bool, error)
}

// liveCluster is internal/cluster itself.
type liveCluster struct{}

func (liveCluster) Install(ctx context.Context, cfg *rest.Config, o cluster.Options) error {
	return cluster.Install(ctx, cfg, o)
}

func (liveCluster) AddApp(ctx context.Context, cfg *rest.Config, o cluster.AppOptions) error {
	return cluster.AddApp(ctx, cfg, o)
}

func (liveCluster) RemoveApp(ctx context.Context, cfg *rest.Config, app string, timeout time.Duration,
	rep progress.Reporter) (bool, error) {
	return cluster.RemoveApp(ctx, cfg, app, timeout, rep)
}

func (liveCluster) Redeploy(ctx context.Context, cfg *rest.Config, app string, timeout time.Duration,
	rep progress.Reporter) error {
	return cluster.Redeploy(ctx, cfg, app, timeout, rep)
}

func (liveCluster) AppSecrets(ctx context.Context, cfg *rest.Config, app string) (map[string]string, error) {
	return cluster.AppSecrets(ctx, cfg, app)
}

func (liveCluster) AppNames(ctx context.Context, cfg *rest.Config) ([]string, error) {
	return cluster.AppNames(ctx, cfg)
}

func (liveCluster) AppStates(ctx context.Context, cfg *rest.Config) ([]cluster.AppState, error) {
	return cluster.AppStates(ctx, cfg)
}

func (liveCluster) AppComponents(ctx context.Context, cfg *rest.Config, app string) ([]cluster.Component, error) {
	return cluster.AppComponents(ctx, cfg, app)
}

func (liveCluster) AppDiagnosis(ctx context.Context, cfg *rest.Config, app string) (cluster.Diagnosis, error) {
	return cluster.AppDiagnosis(ctx, cfg, app)
}

func (liveCluster) Settings(ctx context.Context, cfg *rest.Config) (cluster.Settings, error) {
	return cluster.ClusterSettings(ctx, cfg)
}

func (liveCluster) AppConfig(ctx context.Context, cfg *rest.Config, app string) (*cluster.AppConfig, error) {
	return cluster.ReadAppConfig(ctx, cfg, app)
}

func (liveCluster) RegistryConnections(ctx context.Context, cfg *rest.Config) ([]cluster.RegistryConnection, error) {
	return cluster.RegistryConnections(ctx, cfg)
}

func (liveCluster) RegistryConnection(ctx context.Context, cfg *rest.Config, name string) (*cluster.RegistryAuth, error) {
	return cluster.ReadRegistryConnection(ctx, cfg, name)
}

func (liveCluster) SaveRegistryConnection(ctx context.Context, cfg *rest.Config, name string,
	auth cluster.RegistryAuth, rep progress.Reporter) error {
	return cluster.SaveRegistryConnection(ctx, cfg, name, auth, rep)
}

func (liveCluster) DeleteRegistryConnection(ctx context.Context, cfg *rest.Config, name string,
	rep progress.Reporter) (bool, error) {
	return cluster.DeleteRegistryConnection(ctx, cfg, name, rep)
}

// FetchFunc reads a deploy artifact from a registry.
type FetchFunc func(ctx context.Context, ref string, auth authn.Authenticator, insecure bool) (*schema.App, error)

// Ops performs the operations against one cluster. The three fields below Env are the seams
// tests replace; New fills them with the real implementations. They are fields rather than
// package-level variables so that two callers — a command and a job in the server — never
// interfere with each other.
type Ops struct {
	Target Target
	Env    Env

	Cluster       Cluster
	Fetch         FetchFunc
	NewCloudflare func(token string) CloudflareAPI
}

// New returns the operations against a cluster, talking to the real world.
func New(t Target, env Env) *Ops {
	return &Ops{
		Target:        t,
		Env:           env,
		Cluster:       liveCluster{},
		Fetch:         deploy.Fetch,
		NewCloudflare: liveCloudflare,
	}
}

// config is the cluster connection every operation hands to internal/cluster.
func (o *Ops) config() *rest.Config { return o.Target.Config }

var appNameRE = regexp.MustCompile(schema.AppNamePattern)

// CheckAppName rejects a name that cannot become a namespace and a host name.
func CheckAppName(name string) error {
	if len(name) > schema.MaxAppNameLength || !appNameRE.MatchString(name) {
		return fmt.Errorf("app name %q must be a DNS label of at most %d characters", name, schema.MaxAppNameLength)
	}
	return nil
}
