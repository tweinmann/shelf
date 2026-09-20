package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/deploy"
	"github.com/tweinmann/shelf/internal/progress"
	"github.com/tweinmann/shelf/internal/secrets"
)

// AddOptions configure AddApp.
type AddOptions struct {
	Name     string
	Artifact cluster.Artifact
	// Insecure allows a deploy artifact registry without TLS.
	Insecure bool
	Timeout  time.Duration
}

// AddApp registers an app and waits until it runs: it reads the deploy artifact, works out the
// app's secret values, writes them to the backup and to the cluster, and publishes the app's
// host name if the cluster is exposed. Running it again updates the artifact reference, keeps
// every value that exists and generates the ones that were added to app.yaml since.
func (o *Ops) AddApp(ctx context.Context, opts AddOptions, report progress.Reporter) error {
	rep := progress.OrDiscard(report)
	app, err := o.Fetch(ctx, opts.Artifact.Reference(), o.Env.Pull, opts.Insecure)
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

	publisher, err := o.newDNS(ctx, rep)
	if err != nil {
		return err
	}
	if err := o.Cluster.AddApp(ctx, o.config(), cluster.AppOptions{
		Name:     opts.Name,
		Artifact: opts.Artifact,
		Insecure: opts.Insecure,
		Secrets:  values,
		Timeout:  opts.Timeout,
		Report:   rep,
	}); err != nil {
		return err
	}
	return publisher.publish(ctx, opts.Name, rep)
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
// name. The secret backup on this machine is kept, and the caller is told where it is.
func (o *Ops) RemoveApp(ctx context.Context, name string, timeout time.Duration,
	report progress.Reporter) error {
	rep := progress.OrDiscard(report)
	publisher, err := o.newDNS(ctx, rep)
	if err != nil {
		return err
	}
	found, err := o.Cluster.RemoveApp(ctx, o.config(), name, timeout, rep)
	if err != nil {
		return err
	}
	if err := publisher.withdraw(ctx, name, rep); err != nil {
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
