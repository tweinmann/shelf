package ops

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tweinmann/shelf/internal/cloudflare"
	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/progress"
)

// CloudflareAPI is the part of the Cloudflare API shelf uses.
type CloudflareAPI interface {
	VerifyToken(ctx context.Context) error
	ZoneFor(ctx context.Context, name string) (*cloudflare.Zone, error)
	EnsureRecord(ctx context.Context, zone, name, target string) (cloudflare.Action, error)
	DeleteRecord(ctx context.Context, zone, name string) (bool, error)
	AccountID(ctx context.Context) (string, error)
	FindTunnel(ctx context.Context, account, name string) (*cloudflare.Tunnel, error)
	CreateTunnel(ctx context.Context, account, name string) (*cloudflare.Tunnel, []byte, error)
	DeleteTunnel(ctx context.Context, account, id string) error
}

func liveCloudflare(token string) CloudflareAPI { return cloudflare.New(token) }

// ExposeOptions configure an exposure.
type ExposeOptions struct {
	// TunnelName is the Cloudflare tunnel to use; empty means shelf<host suffix>.
	TunnelName string
	Timeout    time.Duration
}

// ExposePlan is what an exposure would do. PlanExpose produces it, the caller shows it and asks
// whatever it has to ask, and Expose carries it out.
type ExposePlan struct {
	Settings cluster.Settings
	// Hosts is the pattern the apps answer under.
	Hosts string
	// Tunnel is the tunnel the cluster will use.
	Tunnel *cloudflare.Tunnel
	// Name is the tunnel's name, also when the tunnel does not exist yet.
	Name string
	// Credentials is the credentials.json of a tunnel that was just created; nil when an
	// existing tunnel is reused, because its secret cannot be read a second time.
	Credentials []byte
	// Action is "created" when the tunnel was made for this plan, "replaced" after
	// ReplaceTunnel, and empty when an existing tunnel is reused.
	Action string
	// NeedsReplacement means the tunnel exists but the cluster does not hold its credentials,
	// so it is unusable until ReplaceTunnel replaces it. That destroys the old tunnel, which
	// is why it is a separate step the caller has to ask for.
	NeedsReplacement bool

	timeout time.Duration
	api     CloudflareAPI
	account string
}

// Target is where the DNS records of this plan point.
func (p *ExposePlan) Target() string { return p.Tunnel.Target() }

// PlanExpose works out how this cluster reaches the internet: it checks the token, finds the
// account and the tunnel, and creates the tunnel if there is none. It changes nothing in the
// cluster.
func (o *Ops) PlanExpose(ctx context.Context, opts ExposeOptions) (*ExposePlan, error) {
	settings, err := o.Cluster.Settings(ctx, o.config())
	if err != nil {
		return nil, err
	}
	if settings.Domain == "" {
		return nil, fmt.Errorf("this cluster has no domain; run `shelf init cluster --domain <domain>` first")
	}
	name := opts.TunnelName
	if name == "" {
		name = "shelf" + settings.HostSuffix
	}
	plan := &ExposePlan{
		Settings: settings,
		Hosts:    Hosts(settings.Domain, settings.HostSuffix),
		Name:     name,
		timeout:  opts.Timeout,
	}

	api := o.NewCloudflare(o.Env.CloudflareToken)
	if err := api.VerifyToken(ctx); err != nil {
		return nil, fmt.Errorf("%s was refused: %w", EnvCloudflareToken, err)
	}
	plan.api = api
	plan.account = o.Env.CloudflareAccount
	if plan.account == "" {
		if plan.account, err = api.AccountID(ctx); err != nil {
			return nil, err
		}
	}

	existing, err := api.FindTunnel(ctx, plan.account, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		if plan.Tunnel, plan.Credentials, err = api.CreateTunnel(ctx, plan.account, name); err != nil {
			return nil, err
		}
		plan.Action = "created"
		return plan, nil
	}
	// A tunnel without credentials in the cluster is useless: Cloudflare hands out the secret
	// only once, so it cannot be recovered.
	stored, err := o.Cluster.TunnelCredentials(ctx, o.config())
	if err != nil {
		return nil, err
	}
	plan.Tunnel = existing
	plan.NeedsReplacement = !(len(stored) > 0 && strings.Contains(string(stored), existing.ID))
	return plan, nil
}

// ReplaceTunnel destroys the tunnel of a plan that needs replacing and creates a new one under
// the same name. Only the caller knows whether that is wanted, so it has to ask first.
func (o *Ops) ReplaceTunnel(ctx context.Context, plan *ExposePlan) error {
	if err := plan.api.DeleteTunnel(ctx, plan.account, plan.Tunnel.ID); err != nil {
		return fmt.Errorf("replacing the tunnel: %w; a tunnel with open connections cannot be deleted", err)
	}
	tunnel, credentials, err := plan.api.CreateTunnel(ctx, plan.account, plan.Name)
	if err != nil {
		return err
	}
	plan.Tunnel, plan.Credentials = tunnel, credentials
	plan.Action = "replaced"
	plan.NeedsReplacement = false
	return nil
}

// Expose stores the tunnel credentials in the cluster, switches cloudflared on, and publishes
// the host names of the apps that already run. Apps added later are published by AddApp.
func (o *Ops) Expose(ctx context.Context, plan *ExposePlan, report progress.Reporter) error {
	rep := progress.OrDiscard(report)
	if err := o.Cluster.Expose(ctx, o.config(), cluster.ExposeOptions{
		TunnelID:    plan.Tunnel.ID,
		Credentials: plan.Credentials,
		Timeout:     plan.timeout,
		Report:      rep,
	}); err != nil {
		return err
	}
	return o.publishApps(ctx, plan, rep)
}

// publishApps points the host name of every app in this cluster at the tunnel, so an exposure
// that is set up or renewed after the apps reaches them too.
func (o *Ops) publishApps(ctx context.Context, plan *ExposePlan, rep progress.Reporter) error {
	apps, err := o.Cluster.AppNames(ctx, o.config())
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		return nil
	}
	zone, err := plan.api.ZoneFor(ctx, plan.Settings.Domain)
	if err != nil {
		return err
	}
	target := plan.Target()
	for _, app := range apps {
		host := AppHost(plan.Settings, app)
		action, err := plan.api.EnsureRecord(ctx, zone.ID, host, target)
		if err != nil {
			return err
		}
		rep.Report(progress.Record(host, target, string(action)))
	}
	return nil
}
