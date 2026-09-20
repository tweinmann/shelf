package ops

import (
	"context"

	"github.com/tweinmann/shelf/internal/cluster"
)

// App is an app as a caller wants to show it: its state in the cluster, plus where it answers.
type App struct {
	cluster.AppState
	// Host is the name the app answers under, empty when the cluster has no domain yet.
	Host string `json:"host,omitempty"`
	// URL is where a browser reaches the app, empty until the cluster is exposed.
	URL string `json:"url,omitempty"`
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
	// Hosts is the pattern the apps answer under, such as <app>-dev.example.com.
	Hosts string `json:"hosts,omitempty"`
	// Public is false when the domain cannot exist on the internet, as dev.local cannot. The
	// apps then answer inside the cluster only, whatever else is set up.
	Public bool `json:"public"`
	// Exposed is true once a tunnel carries the apps to the internet.
	Exposed bool `json:"exposed"`
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
	s.Installed = settings.Domain != ""
	s.Hosts = Hosts(settings.Domain, settings.HostSuffix)
	s.Public = PublicDomain(settings.Domain)
	s.Exposed = s.Public && settings.TunnelTarget != ""
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
		if a.Name == name {
			return a, nil
		}
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
	if settings.Domain == "" {
		return a
	}
	a.Host = AppHost(settings, state.Name)
	// Only a name that exists on the internet and a tunnel that carries it make a link that
	// works; otherwise the host name is a fact about the cluster, not an address.
	if settings.TunnelTarget != "" && PublicDomain(settings.Domain) {
		a.URL = "https://" + a.Host + "/"
	}
	return a
}
