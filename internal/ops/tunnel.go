package ops

import (
	"bytes"
	"context"
	"fmt"

	"github.com/tweinmann/shelf/internal/cloudflare"
	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/progress"
)

// CloudflareAPI is the part of the Cloudflare API shelf uses.
type CloudflareAPI interface {
	VerifyToken(ctx context.Context) error
	Zones(ctx context.Context, account string) ([]cloudflare.Zone, error)
	ZoneFor(ctx context.Context, name string) (*cloudflare.Zone, error)
	EnsureRecord(ctx context.Context, zone, name, target string) (cloudflare.Action, error)
	DeleteRecord(ctx context.Context, zone, name string) (bool, error)
	AccountID(ctx context.Context) (string, error)
	FindTunnel(ctx context.Context, account, name string) (*cloudflare.Tunnel, error)
	CreateTunnel(ctx context.Context, account, name string) (*cloudflare.Tunnel, []byte, error)
	DeleteTunnel(ctx context.Context, account, id string) error
}

func liveCloudflare(token string) CloudflareAPI { return cloudflare.New(token) }

// AppHost is where an app answers: <app><host suffix>.<domain>, or <app>.shelf.internal inside
// the cluster when it has no domain of its own.
func AppHost(app, suffix, domain string) string {
	if domain == "" {
		return app + "." + cluster.InternalDomain
	}
	return app + suffix + "." + domain
}

// TunnelName is the Cloudflare tunnel of an app. The host suffix is part of it, so the apps of
// two clusters that share an account do not take each other's tunnel.
func TunnelName(app, suffix string) string { return "shelf" + suffix + "-" + app }

// exposure is how an app reaches the internet, before and after a change. planExposure works it
// out and creates what has to exist before the cluster refers to it — the new tunnel —, and
// finish cleans up what the cluster no longer refers to — the old record and the old tunnel.
type exposure struct {
	app string
	// tunnelName is what the app's tunnel is called, before the change and after it.
	tunnelName string

	// Before the change: the tunnel the cluster runs, the name that points at it, and the
	// Cloudflare connection that made it, which is nil when this machine does not hold it.
	oldTunnel string
	oldHost   string
	oldAccess *hostcfg.Cloudflare

	// After the change. Without a connection the app is not exposed, unless keep is set.
	host   string
	access *hostcfg.Cloudflare
	api    CloudflareAPI
	zone   string
	tunnel *cloudflare.Tunnel
	// credentials is the credentials.json of a tunnel created for this change, nil when the
	// cluster keeps the one it has.
	credentials []byte
	// keep means the app is exposed, but this machine does not hold its connection: the tunnel
	// stays as it is, and no record is touched.
	keep bool
	// replaced is a tunnel that was deleted while planning, so finish does not try again.
	replaced string
}

// tunnelID is the tunnel the cluster runs for the app after the change.
func (e *exposure) tunnelID() string {
	switch {
	case e.keep:
		return e.oldTunnel
	case e.tunnel != nil:
		return e.tunnel.ID
	}
	return ""
}

// planExposure finds or creates the app's tunnel in the account of the Cloudflare connection the
// app is exposed through after the change; an empty connection means it is not exposed. It
// changes nothing in the cluster.
func (o *Ops) planExposure(ctx context.Context, name string, settings cluster.Settings,
	current *cluster.AppConfig, domain, connection string, rep progress.Reporter) (*exposure, error) {
	old, err := o.Env.Connections.Cloudflare(current.Cloudflare)
	if err != nil {
		return nil, err
	}
	e := &exposure{
		app:        name,
		tunnelName: TunnelName(name, settings.HostSuffix),
		oldTunnel:  current.TunnelID,
		oldHost:    AppHost(name, settings.HostSuffix, current.Domain),
		oldAccess:  old,
		host:       AppHost(name, settings.HostSuffix, domain),
	}
	if connection == "" {
		return e, nil
	}
	if connection == current.Cloudflare {
		e.access = old
	} else if e.access, err = o.Env.Connections.Cloudflare(connection); err != nil {
		return nil, err
	}
	if e.access == nil {
		if connection == current.Cloudflare && current.TunnelID != "" {
			e.keep = true
			rep.Report(progress.Warning(fmt.Sprintf("%s is exposed through the Cloudflare connection %s, which this machine does not\n"+
				"hold; its tunnel and the record of %s were left as they are. Define the connection\n"+
				"here with `shelf connection add cloudflare %s` to change them.\n", name, connection, e.host, connection)))
			return e, nil
		}
		return nil, missingCloudflare(connection)
	}

	if domain == "" {
		return nil, noDomain(name, connection)
	}
	if !PublicDomain(domain) {
		return nil, fmt.Errorf("%s cannot be exposed under %s, which is not a public domain; give it a public domain of its own",
			name, domain)
	}
	e.api = o.NewCloudflare(e.access.Token)
	zone, err := e.api.ZoneFor(ctx, domain)
	if err != nil {
		return nil, err
	}
	e.zone = zone.ID

	subject := "Cloudflare tunnel " + e.tunnelName
	existing, err := e.api.FindTunnel(ctx, e.access.Account, e.tunnelName)
	if err != nil {
		return nil, err
	}
	if existing != nil && bytes.Contains(current.TunnelCredentials, []byte(existing.ID)) {
		e.tunnel = existing
		rep.Report(progress.Applied(subject, "unchanged", ""))
		return e, nil
	}
	action := "created"
	if existing != nil {
		// Cloudflare hands out a tunnel's secret only once. A tunnel of this name whose
		// credentials the cluster does not hold is useless, and it can only be this app's.
		if err := e.api.DeleteTunnel(ctx, e.access.Account, existing.ID); err != nil {
			return nil, fmt.Errorf("replacing %s, whose credentials this cluster does not hold: %w", subject, err)
		}
		e.replaced, action = existing.ID, "replaced"
	}
	if e.tunnel, e.credentials, err = e.api.CreateTunnel(ctx, e.access.Account, e.tunnelName); err != nil {
		return nil, err
	}
	rep.Report(progress.Applied(subject, action, ""))
	return e, nil
}

// finish points the app's name at its tunnel, and removes the record and the tunnel the cluster
// no longer uses. It runs once cloudflared has moved, because Cloudflare refuses to delete a
// tunnel that still has connections.
func (o *Ops) finish(ctx context.Context, e *exposure, rep progress.Reporter) error {
	if e.keep {
		return nil
	}
	if e.oldTunnel != "" {
		accountChanged := e.access != nil && e.oldAccess != nil && e.oldAccess.Account != e.access.Account
		if e.access == nil || e.oldHost != e.host || accountChanged {
			if err := o.withdrawRecord(ctx, e.app, e.oldHost, e.oldAccess, rep); err != nil {
				return err
			}
		}
	}
	if e.access != nil {
		target := e.tunnel.Target()
		action, err := e.api.EnsureRecord(ctx, e.zone, e.host, target)
		if err != nil {
			return err
		}
		rep.Report(progress.Record(e.host, target, string(action)))
	}
	if e.oldTunnel != "" && e.oldTunnel != e.tunnelID() && e.oldTunnel != e.replaced {
		if err := o.deleteTunnel(ctx, e.app, e.tunnelName, e.oldTunnel, e.oldAccess, rep); err != nil {
			return err
		}
	}
	return nil
}

// withdrawRecord removes the record of an app's host name. Without the access that made it, it
// names the record, so that it can be deleted by hand.
func (o *Ops) withdrawRecord(ctx context.Context, app, host string, access *hostcfg.Cloudflare,
	rep progress.Reporter) error {
	if access == nil {
		rep.Report(progress.Warning(fmt.Sprintf("This machine does not hold the Cloudflare connection of %s, so the record of\n"+
			"%s stays in Cloudflare; delete it there.\n", app, host)))
		return nil
	}
	api := o.NewCloudflare(access.Token)
	zone, err := api.ZoneFor(ctx, domainOf(host))
	if err != nil {
		return err
	}
	found, err := api.DeleteRecord(ctx, zone.ID, host)
	if err != nil {
		return err
	}
	if found {
		rep.Report(progress.Record(host, "", "deleted"))
	}
	return nil
}

// deleteTunnel removes a tunnel the app no longer uses. Without the access that made it, it
// names the tunnel, so that it can be deleted by hand.
func (o *Ops) deleteTunnel(ctx context.Context, app, name, id string, access *hostcfg.Cloudflare,
	rep progress.Reporter) error {
	if access == nil {
		rep.Report(progress.Warning(fmt.Sprintf("This machine does not hold the Cloudflare connection of %s, so its tunnel\n"+
			"%s (%s) stays in Cloudflare; delete it there.\n", app, name, id)))
		return nil
	}
	if err := o.NewCloudflare(access.Token).DeleteTunnel(ctx, access.Account, id); err != nil {
		return fmt.Errorf("deleting the Cloudflare tunnel %s of %s: %w", name, app, err)
	}
	rep.Report(progress.Applied("Cloudflare tunnel "+name, "deleted", ""))
	return nil
}

// domainOf is the domain of a host name that AppHost made: everything after the first label.
func domainOf(host string) string {
	for i := range len(host) {
		if host[i] == '.' {
			return host[i+1:]
		}
	}
	return host
}

// noDomain is why an app cannot go through a Cloudflare connection without a domain of its own:
// its record has to live in a zone of that connection's account.
func noDomain(app, connection string) error {
	return fmt.Errorf("%s needs a domain of its own to be exposed through the Cloudflare connection %s: "+
		"one of the zones of that account (--domain)", app, connection)
}
