package ops

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/deploy"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/progress"
	"github.com/tweinmann/shelf/internal/secrets"
)

// pullAuth is the login for reading a deploy artifact from here. An explicit one wins. Otherwise
// the app's own login is used, because it is what the cluster pulls the artifact with, and it
// spares the machine running shelf a Docker config it may not have. It is only offered to the
// registry it is for; a nil result leaves the Docker config to answer.
func (o *Ops) pullAuth(artifact cluster.Artifact, login *cluster.RegistryAuth) authn.Authenticator {
	if o.Env.Pull != nil {
		return o.Env.Pull
	}
	if login == nil || artifact.Registry() != cluster.RegistryHost {
		return nil
	}
	return authn.FromConfig(authn.AuthConfig{Username: login.Username, Password: login.Token})
}

// Access is how an app reaches its registry and the internet. Every field is optional: what is
// not given stays as the app has it.
type Access struct {
	// Domain gives the app a domain of its own instead of the cluster's.
	Domain string
	// Registry is the app's login for ghcr.io, for its deploy artifact and its images.
	Registry *cluster.RegistryAuth
	// RemoveRegistry drops the login; the app then reads its registry without one.
	RemoveRegistry bool
	// Cloudflare exposes the app through a tunnel of its own in that account. The token needs
	// Account:Cloudflare Tunnel:Edit, and Zone:DNS:Edit for the app's zone. An empty account
	// means the only one the token sees.
	Cloudflare *hostcfg.Cloudflare
	// RemoveCloudflare takes the app off the internet: its record and its tunnel are deleted.
	RemoveCloudflare bool
}

// Check rejects access that contradicts itself or cannot work. The messages carry no field
// name, so a caller can put its own in front.
func (a Access) Check() error {
	if a.Domain != "" {
		if err := CheckDomain(a.Domain); err != nil {
			return fmt.Errorf("the domain %w", err)
		}
	}
	if a.Registry != nil {
		if a.RemoveRegistry {
			return errors.New("a registry login cannot be given and removed at once")
		}
		if a.Registry.Username == "" || a.Registry.Token == "" {
			return errors.New("a registry login needs a user name and a token")
		}
	}
	if a.Cloudflare != nil {
		if a.RemoveCloudflare {
			return errors.New("Cloudflare access cannot be given and removed at once")
		}
		if strings.TrimSpace(a.Cloudflare.Token) == "" {
			return errors.New("Cloudflare access needs an API token")
		}
	}
	return nil
}

// AddOptions configure AddApp.
type AddOptions struct {
	Name     string
	Artifact cluster.Artifact
	// Insecure allows a deploy artifact registry without TLS.
	Insecure bool
	// Access changes how the app reaches its registry and the internet.
	Access  Access
	Timeout time.Duration
}

// AddApp registers an app and waits until it runs: it reads the deploy artifact, works out the
// app's secret values, writes them to the backup and to the cluster, and exposes the app through
// its own tunnel if it has Cloudflare access. Running it again updates the artifact reference,
// keeps every value that exists, generates the ones that were added to app.yaml since, and
// keeps the app's access except where it is changed.
func (o *Ops) AddApp(ctx context.Context, opts AddOptions, report progress.Reporter) error {
	rep := progress.OrDiscard(report)
	if err := opts.Access.Check(); err != nil {
		return err
	}
	current, err := o.Cluster.AppConfig(ctx, o.config(), opts.Name)
	if err != nil {
		return err
	}
	if current == nil {
		current = &cluster.AppConfig{}
	}
	registry := current.Registry
	switch {
	case opts.Access.RemoveRegistry:
		registry = nil
		rep.Report(progress.Info("registry login: none"))
	case opts.Access.Registry != nil:
		registry = opts.Access.Registry
		rep.Report(progress.Info("registry login: %s for %s", registry.Username, cluster.RegistryHost))
	}

	app, err := o.Fetch(ctx, opts.Artifact.Reference(), o.pullAuth(opts.Artifact, registry), opts.Insecure)
	if err != nil {
		return err
	}
	if app.Name != opts.Name {
		return fmt.Errorf("the artifact deploys app %q, not %q", app.Name, opts.Name)
	}

	stored, err := o.Cluster.AppSecrets(ctx, o.config(), opts.Name)
	if err != nil {
		return err
	}
	saved, err := o.Env.Backup.Load(opts.Name)
	if err != nil {
		return err
	}
	declared := deploy.SecretNames(app)
	values, sources := secrets.Merge(declared, stored, saved)
	for _, s := range declared {
		rep.Report(progress.Secret(s, string(sources[s])))
	}
	// The backup is written first, so a generated value never exists only in the cluster.
	if len(values) > 0 {
		if err := o.Env.Backup.Save(opts.Name, values); err != nil {
			return fmt.Errorf("writing the secret backup: %w", err)
		}
		rep.Report(progress.Info("secret backup: %s", o.Env.Backup.Path(opts.Name)))
	}

	settings, err := o.Cluster.Settings(ctx, o.config())
	if err != nil {
		return err
	}
	domain := cmp.Or(opts.Access.Domain, current.Domain)
	exposure, err := o.planExposure(ctx, opts.Name, settings, current, cmp.Or(domain, settings.Domain),
		opts.Access, rep)
	if err != nil {
		return err
	}
	if err := o.Cluster.AddApp(ctx, o.config(), cluster.AppOptions{
		Name:              opts.Name,
		Artifact:          opts.Artifact,
		Insecure:          opts.Insecure,
		Domain:            domain,
		Registry:          registry,
		TunnelID:          exposure.tunnelID(),
		TunnelCredentials: exposure.credentials,
		Secrets:           values,
		Timeout:           opts.Timeout,
		Report:            rep,
	}); err != nil {
		return err
	}
	return o.finish(ctx, exposure, rep)
}

// SetAccess changes how an app reaches its registry and the internet, and nothing else: it
// deploys the artifact the app is registered with, the same way AddApp does.
func (o *Ops) SetAccess(ctx context.Context, name string, access Access, timeout time.Duration,
	report progress.Reporter) error {
	current, err := o.Cluster.AppConfig(ctx, o.config(), name)
	if err != nil {
		return err
	}
	if current == nil {
		return &NotFoundError{Name: name}
	}
	return o.AddApp(ctx, AddOptions{
		Name:     name,
		Artifact: current.Artifact,
		Insecure: current.Insecure,
		Access:   access,
		Timeout:  timeout,
	}, report)
}

// Redeploy asks Flux to fetch the app's artifact again and to roll out what it finds. It is
// what to do when a tag was moved, or when something failed and is worth another try.
func (o *Ops) Redeploy(ctx context.Context, name string, timeout time.Duration,
	report progress.Reporter) error {
	return o.Cluster.Redeploy(ctx, o.config(), name, timeout, progress.OrDiscard(report))
}

// Secrets returns the generated secret values of an app, by name. They are generated, so the
// person who runs the app has no other way to learn them — a database client needs the
// password that the app itself gets from the environment.
func (o *Ops) Secrets(ctx context.Context, name string) (map[string]string, error) {
	return o.Cluster.AppSecrets(ctx, o.config(), name)
}

// RemoveApp removes an app with its namespace, volumes and secrets, and withdraws its host
// name and its tunnel from Cloudflare. The secret backup on this machine is kept, and the caller
// is told where it is.
func (o *Ops) RemoveApp(ctx context.Context, name string, timeout time.Duration,
	report progress.Reporter) error {
	rep := progress.OrDiscard(report)
	current, err := o.Cluster.AppConfig(ctx, o.config(), name)
	if err != nil {
		return err
	}
	settings, err := o.Cluster.Settings(ctx, o.config())
	if err != nil {
		return err
	}
	access, err := o.Env.Access.Cloudflare(name)
	if err != nil {
		return err
	}
	found, err := o.Cluster.RemoveApp(ctx, o.config(), name, timeout, rep)
	if err != nil {
		return err
	}
	// cloudflared went with the namespace, so the tunnel has no connections left.
	if current != nil && current.TunnelID != "" {
		host := AppHost(name, settings.HostSuffix, cmp.Or(current.Domain, settings.Domain))
		if err := o.withdrawRecord(ctx, name, host, access, rep); err != nil {
			return err
		}
		if err := o.deleteTunnel(ctx, name, TunnelName(name, settings.HostSuffix), current.TunnelID,
			access, rep); err != nil {
			return err
		}
	}
	if err := o.Env.Access.DeleteCloudflare(name); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("app %s does not exist", name)
	}
	path := o.Env.Backup.Path(name)
	switch _, err := os.Stat(path); {
	case err == nil:
		rep.Report(progress.Info(
			"The secret backup stays in %s; delete it if you do not need the values any more.", path))
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	return nil
}
