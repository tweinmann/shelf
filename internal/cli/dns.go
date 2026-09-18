package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/cluster"
)

// appHost is where an app answers: <app><host suffix>.<domain>.
func appHost(s cluster.Settings, app string) string {
	return app + s.HostSuffix + "." + s.Domain
}

// dns publishes the host names of apps. It is nil when the cluster is not exposed or no token is
// available, and then nothing is published.
type dns struct {
	api      cloudflareAPI
	zone     string
	settings cluster.Settings
}

// newDNS prepares the DNS side of an app command. An unexposed cluster and a missing token are
// not errors: the app still runs, it is only not reachable from the internet.
func newDNS(ctx context.Context, cfg *rest.Config, out io.Writer) (*dns, error) {
	settings, err := clusterSettings(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if settings.TunnelTarget == "" {
		return nil, nil
	}
	token := strings.TrimSpace(os.Getenv(envCloudflareToken))
	if token == "" {
		fmt.Fprintf(out, "DNS: skipped, %s is not set; run `shelf init expose` with it to publish the host names\n",
			envCloudflareToken)
		return nil, nil
	}
	api := newCloudflare(token)
	zone, err := api.ZoneFor(ctx, settings.Domain)
	if err != nil {
		return nil, err
	}
	return &dns{api: api, zone: zone.ID, settings: settings}, nil
}

// publish points the app's host name at the tunnel.
func (d *dns) publish(ctx context.Context, app string, out io.Writer) error {
	if d == nil {
		return nil
	}
	host := appHost(d.settings, app)
	action, err := d.api.EnsureRecord(ctx, d.zone, host, d.settings.TunnelTarget)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "DNS %s -> %s: %s\n", host, d.settings.TunnelTarget, action)
	return nil
}

// withdraw removes the app's host name again.
func (d *dns) withdraw(ctx context.Context, app string, out io.Writer) error {
	if d == nil {
		return nil
	}
	host := appHost(d.settings, app)
	found, err := d.api.DeleteRecord(ctx, d.zone, host)
	if err != nil {
		return err
	}
	if found {
		fmt.Fprintf(out, "DNS %s: deleted\n", host)
	}
	return nil
}
