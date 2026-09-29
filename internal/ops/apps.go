package ops

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/deploy"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/progress"
	"github.com/tweinmann/shelf/internal/secrets"
)

// pullAuth is the login for reading a deploy artifact from here. An explicit one wins. Otherwise
// the login of the app's registry connection is used, because it is what the cluster pulls the
// artifact with, and it spares the machine running shelf a Docker config it may not have. It is
// only offered to the registry it is for; a nil result leaves the Docker config to answer.
func (o *Ops) pullAuth(artifact cluster.Artifact, login *cluster.RegistryAuth) authn.Authenticator {
	if o.Env.Pull != nil {
		return o.Env.Pull
	}
	if login == nil || artifact.Registry() != cluster.RegistryHost {
		return nil
	}
	return authn.FromConfig(authn.AuthConfig{Username: login.Username, Password: login.Token})
}

// Access is how an app reaches its registry and the internet: a domain and the connections it
// uses, by name. Every field is optional: what is not given stays as the app has it.
type Access struct {
	// Domain gives the app a domain of its own instead of the cluster's.
	Domain string
	// Registry is the registry connection the app pulls its deploy artifact and its images with.
	Registry string
	// RemoveRegistry drops the registry connection; the app then pulls without a login.
	RemoveRegistry bool
	// Cloudflare is the Cloudflare connection the app is exposed through, with a tunnel of its
	// own in that connection's account.
	Cloudflare string
	// Quick exposes the app through a quick tunnel at a random trycloudflare.com name instead:
	// no Cloudflare connection, no domain of its own needed. A tunnel and a record the app had
	// through a connection are deleted.
	Quick bool
	// Private takes the app off the internet: it answers inside the cluster only, and a tunnel
	// and a record it had are deleted. It is what a new app gets when nothing else is chosen.
	Private bool
}

// exposures counts how many ways to reach the internet an Access asks for at once.
func (a Access) exposures() int {
	n := 0
	for _, set := range []bool{a.Cloudflare != "", a.Quick, a.Private} {
		if set {
			n++
		}
	}
	return n
}

// Check rejects access that contradicts itself. The messages carry no field name, so a caller
// can put its own in front.
func (a Access) Check() error {
	if a.Domain != "" {
		if err := CheckDomain(a.Domain); err != nil {
			return fmt.Errorf("the domain %w", err)
		}
	}
	if a.Registry != "" {
		if a.RemoveRegistry {
			return errors.New("a registry connection cannot be given and removed at once")
		}
		if err := CheckConnectionName(a.Registry); err != nil {
			return err
		}
	}
	if a.exposures() > 1 {
		return errors.New("an app is exposed through a Cloudflare connection, through a quick tunnel, or not at all; choose one")
	}
	if a.Cloudflare != "" {
		if err := CheckConnectionName(a.Cloudflare); err != nil {
			return err
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
// its own tunnel if it has a Cloudflare connection, or through a quick tunnel if it asks for one;
// otherwise it stays inside the cluster. Running it again updates the artifact reference, keeps every value that exists,
// generates the ones that were added to app.yaml since, and keeps the app's access except where
// it is changed.
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
	registry, cloudflare, quick := current.Registry, current.Cloudflare, current.Quick
	switch {
	case opts.Access.RemoveRegistry:
		registry = ""
		rep.Report(progress.Info("registry connection: none"))
	case opts.Access.Registry != "":
		registry = opts.Access.Registry
		rep.Report(progress.Info("registry connection: %s", registry))
	}
	switch {
	case opts.Access.Private:
		cloudflare, quick = "", false
		rep.Report(progress.Info("internet: none, inside the cluster only"))
	case opts.Access.Quick:
		cloudflare, quick = "", true
		rep.Report(progress.Info("internet: a quick tunnel"))
	case opts.Access.Cloudflare != "":
		cloudflare, quick = opts.Access.Cloudflare, false
		rep.Report(progress.Info("Cloudflare connection: %s", cloudflare))
	}
	if opts.Access.Cloudflare != "" && opts.Access.Cloudflare != current.Cloudflare {
		conn, err := o.Env.Connections.Cloudflare(opts.Access.Cloudflare)
		if err != nil {
			return err
		}
		if conn == nil {
			return missingCloudflare(opts.Access.Cloudflare)
		}
	}
	var login *cluster.RegistryAuth
	if registry != "" {
		if login, err = o.Cluster.RegistryConnection(ctx, o.config(), registry); err != nil {
			return err
		}
		if login == nil {
			return missingRegistry(registry)
		}
	}

	app, err := o.Fetch(ctx, opts.Artifact.Reference(), o.pullAuth(opts.Artifact, login), opts.Insecure)
	if err != nil {
		return err
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
		cloudflare, rep)
	if err != nil {
		return err
	}
	if err := o.Cluster.AddApp(ctx, o.config(), cluster.AppOptions{
		Name:              opts.Name,
		Artifact:          opts.Artifact,
		Insecure:          opts.Insecure,
		Domain:            domain,
		Registry:          registry,
		Cloudflare:        cloudflare,
		TunnelID:          exposure.tunnelID(),
		TunnelCredentials: exposure.credentials,
		Quick:             quick,
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
	var access *hostcfg.Cloudflare
	if current != nil {
		if access, err = o.Env.Connections.Cloudflare(current.Cloudflare); err != nil {
			return err
		}
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
