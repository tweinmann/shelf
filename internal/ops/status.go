package ops

import (
	"context"
	"fmt"
	"strings"

	"github.com/tweinmann/shelf/internal/cluster"
)

// App is an app as a caller wants to show it: its state in the cluster, plus where it answers.
type App struct {
	cluster.AppState
	// Host is the name the app answers under: <app><suffix>.<domain>, or <app>.shelf.internal
	// without a domain. For an app with a quick tunnel that has its address, it is the tunnel's
	// random name.
	Host string `json:"host,omitempty"`
	// Public is false when the app has no domain, or one that cannot exist on the internet, as
	// dev.local cannot. The app then answers inside the cluster only, unless it has a quick tunnel.
	Public bool `json:"public"`
	// URL is where a browser reaches the app, empty until the app is exposed.
	URL string `json:"url,omitempty"`
	// CloudflareAccount is the account of the app's Cloudflare connection, filled by App but not
	// by Apps, and only when this machine holds the connection. The token never leaves the file
	// it is kept in.
	CloudflareAccount string `json:"cloudflareAccount,omitempty"`
	// CloudflareMissing means the app has a Cloudflare connection this machine does not hold,
	// so its record and tunnel cannot be changed from here. Filled by App.
	CloudflareMissing bool `json:"cloudflareMissing,omitempty"`
	// Components is what the app is made of, filled by App but not by Apps: a list of apps
	// does not need it, and it costs a read per app.
	Components []Component `json:"components,omitempty"`
}

// Component is one part of an app together with the address it answers at.
type Component struct {
	cluster.Component
	// URL is where a browser reaches this component, empty unless it has a route and the
	// cluster carries the app to the internet.
	URL string `json:"url,omitempty"`
	// Address is what URL says without the scheme, or, for a component without a route, how a
	// sibling component reaches it. Empty when there is no address to give at all.
	Address string `json:"address,omitempty"`
	// Internal is true when the component has no route, so nothing outside the app reaches it.
	Internal bool `json:"internal,omitempty"`
}

// Status is what a dashboard shows above the apps. It never fails: a cluster that cannot be
// reached is a status, not an error, because that is exactly when someone looks.
type Status struct {
	// Context and Server say which cluster this is.
	Context string `json:"context"`
	Server  string `json:"server"`
	// Reachable is false when the API server did not answer.
	Reachable bool `json:"reachable"`
	// Error is why it did not answer.
	Error string `json:"error,omitempty"`
	// Installed is false when the cluster has no shelf settings, so `shelf init cluster` has
	// not run against it.
	Installed bool             `json:"installed"`
	Settings  cluster.Settings `json:"-"`
}

// Status reads how the cluster is doing.
func (o *Ops) Status(ctx context.Context) Status {
	s := Status{Context: o.Target.Context}
	if o.Target.Config != nil {
		s.Server = o.Target.Config.Host
	}
	settings, err := o.Cluster.Settings(ctx, o.config())
	if err != nil {
		s.Error = err.Error()
		return s
	}
	s.Reachable = true
	s.Settings = settings
	s.Installed = settings.Installed()
	return s
}

// Apps returns every registered app with its state and its address.
func (o *Ops) Apps(ctx context.Context) ([]App, error) {
	settings, err := o.Cluster.Settings(ctx, o.config())
	if err != nil {
		return nil, err
	}
	states, err := o.Cluster.AppStates(ctx, o.config())
	if err != nil {
		return nil, err
	}
	apps := make([]App, 0, len(states))
	for _, state := range states {
		apps = append(apps, app(state, settings))
	}
	return apps, nil
}

// App returns one app with its state and its address.
func (o *Ops) App(ctx context.Context, name string) (App, error) {
	apps, err := o.Apps(ctx)
	if err != nil {
		return App{}, err
	}
	for _, a := range apps {
		if a.Name != name {
			continue
		}
		list, err := o.Cluster.AppComponents(ctx, o.config(), name)
		if err != nil {
			return App{}, err
		}
		conn, err := o.Env.Connections.Cloudflare(a.Cloudflare)
		if err != nil {
			return App{}, err
		}
		if conn != nil {
			a.CloudflareAccount = conn.Account
		}
		a.CloudflareMissing = a.Cloudflare != "" && conn == nil
		a.Components = components(list, a.Host, a.URL != "")
		return a, nil
	}
	return App{}, &NotFoundError{Name: name}
}

// Diagnose returns the chain from the deploy artifact to the running pods, so that a caller can
// say which step is the reason an app does not work.
func (o *Ops) Diagnose(ctx context.Context, name string) (cluster.Diagnosis, error) {
	return o.Cluster.AppDiagnosis(ctx, o.config(), name)
}

// NotFoundError means there is no app of that name in this cluster.
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string { return "app " + e.Name + " does not exist" }

func app(state cluster.AppState, settings cluster.Settings) App {
	a := App{AppState: state}
	a.Host = AppHost(state.Name, settings.HostSuffix, state.Domain)
	a.Public = PublicDomain(state.Domain)
	switch {
	// A quick tunnel does not need the domain to exist: the app answers under the random name
	// cloudflared was given, as long as it has one.
	case state.Quick && state.QuickURL != "":
		a.URL = state.QuickURL
		a.Host = strings.TrimSuffix(strings.TrimPrefix(state.QuickURL, "https://"), "/")
	// Only a name that exists on the internet and a tunnel that carries it make a link that
	// works; otherwise the host name is a fact about the cluster, not an address.
	case state.Tunnel != "" && a.Public:
		a.URL = "https://" + a.Host + "/"
	}
	return a
}

// components says where each part of an app answers. Whether a link is offered is taken from the
// app's own URL rather than decided again, so the two can never disagree.
func components(list []cluster.Component, host string, exposed bool) []Component {
	out := make([]Component, 0, len(list))
	for _, c := range list {
		comp := Component{Component: c, Internal: c.Path == ""}
		switch {
		case comp.Internal:
			comp.Address = siblingAddress(c)
		case host != "":
			comp.Address = host + c.Path
			if exposed {
				comp.URL = "https://" + comp.Address
			}
		}
		out = append(out, comp)
	}
	return out
}

// siblingAddress is how another component of the same app reaches this one: the chart names the
// Service after the component, in the app's own namespace.
func siblingAddress(c cluster.Component) string {
	parts := make([]string, 0, len(c.Ports))
	for _, p := range c.Ports {
		parts = append(parts, fmt.Sprintf("%s:%d", c.Name, p.Number))
	}
	return strings.Join(parts, ", ")
}
