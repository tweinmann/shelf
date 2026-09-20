package ops

import (
	"context"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/progress"
)

// AppHost is where an app answers: <app><host suffix>.<domain>.
func AppHost(s cluster.Settings, app string) string {
	return app + s.HostSuffix + "." + s.Domain
}

// dns publishes the host names of apps. It is nil when the cluster is not exposed or no token is
// available, and then nothing is published.
type dns struct {
	api      CloudflareAPI
	zone     string
	settings cluster.Settings
}

// newDNS prepares the DNS side of an app operation. An unexposed cluster and a missing token are
// not errors: the app still runs, it is only not reachable from the internet.
func (o *Ops) newDNS(ctx context.Context, rep progress.Reporter) (*dns, error) {
	settings, err := o.Cluster.Settings(ctx, o.config())
	if err != nil {
		return nil, err
	}
	if settings.TunnelTarget == "" {
		return nil, nil
	}
	if o.Env.CloudflareToken == "" {
		rep.Report(progress.Info("DNS: skipped, %s is not set; run `shelf init expose` with it to publish the host names",
			EnvCloudflareToken))
		return nil, nil
	}
	api := o.NewCloudflare(o.Env.CloudflareToken)
	zone, err := api.ZoneFor(ctx, settings.Domain)
	if err != nil {
		return nil, err
	}
	return &dns{api: api, zone: zone.ID, settings: settings}, nil
}

// publish points the app's host name at the tunnel.
func (d *dns) publish(ctx context.Context, app string, rep progress.Reporter) error {
	if d == nil {
		return nil
	}
	host := AppHost(d.settings, app)
	action, err := d.api.EnsureRecord(ctx, d.zone, host, d.settings.TunnelTarget)
	if err != nil {
		return err
	}
	rep.Report(progress.Record(host, d.settings.TunnelTarget, string(action)))
	return nil
}

// withdraw removes the app's host name again.
func (d *dns) withdraw(ctx context.Context, app string, rep progress.Reporter) error {
	if d == nil {
		return nil
	}
	host := AppHost(d.settings, app)
	found, err := d.api.DeleteRecord(ctx, d.zone, host)
	if err != nil {
		return err
	}
	if found {
		rep.Report(progress.Record(host, "", "deleted"))
	}
	return nil
}
