package ops

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/progress"
)

// The kinds of connections. A connection is a login the user defines once, by name, and assigns
// to any number of apps: a registry connection is what an app pulls with, a Cloudflare
// connection what it is exposed through.
const (
	ConnectionRegistry   = "registry"
	ConnectionCloudflare = "cloudflare"
)

// CheckConnectionName rejects a name that cannot be part of a Secret name or a file name.
func CheckConnectionName(name string) error {
	if len(name) > 40 || !appNameRE.MatchString(name) {
		return fmt.Errorf("connection name %q must be a DNS label of at most 40 characters", name)
	}
	return nil
}

func missingRegistry(name string) error {
	return fmt.Errorf("there is no registry connection %s; define it with `shelf connection add registry %s`", name, name)
}

func missingCloudflare(name string) error {
	return fmt.Errorf("this machine holds no Cloudflare connection %s; define it with `shelf connection add cloudflare %s`",
		name, name)
}

// RegistryConnection is a registry connection with the apps that pull with it.
type RegistryConnection struct {
	cluster.RegistryConnection
	Apps []string `json:"apps,omitempty"`
}

// CloudflareConnection is a Cloudflare connection with the apps exposed through it. The token is
// not part of it.
type CloudflareConnection struct {
	Name    string   `json:"name"`
	Account string   `json:"account,omitempty"`
	Apps    []string `json:"apps,omitempty"`
	// Missing means an app uses a connection of this name, but this machine does not hold it.
	Missing bool `json:"missing,omitempty"`
}

// Connections are all connections, of both kinds.
type Connections struct {
	Registry   []RegistryConnection   `json:"registry"`
	Cloudflare []CloudflareConnection `json:"cloudflare"`
}

// usage returns, per connection name, the apps that use it.
func (o *Ops) usage(ctx context.Context) (registry, cloudflare map[string][]string, err error) {
	states, err := o.Cluster.AppStates(ctx, o.config())
	if err != nil {
		return nil, nil, err
	}
	registry, cloudflare = map[string][]string{}, map[string][]string{}
	for _, s := range states {
		if s.Registry != "" {
			registry[s.Registry] = append(registry[s.Registry], s.Name)
		}
		if s.Cloudflare != "" {
			cloudflare[s.Cloudflare] = append(cloudflare[s.Cloudflare], s.Name)
		}
	}
	return registry, cloudflare, nil
}

// Connections lists the registry connections of the cluster and the Cloudflare connections of
// this machine, with the apps that use each. A Cloudflare connection an app uses but this machine
// does not hold is listed as missing.
func (o *Ops) Connections(ctx context.Context) (Connections, error) {
	regUsage, cfUsage, err := o.usage(ctx)
	if err != nil {
		return Connections{}, err
	}
	regs, err := o.Cluster.RegistryConnections(ctx, o.config())
	if err != nil {
		return Connections{}, err
	}
	cfs, err := o.Env.Connections.CloudflareConnections()
	if err != nil {
		return Connections{}, err
	}
	out := Connections{Registry: []RegistryConnection{}, Cloudflare: []CloudflareConnection{}}
	for _, r := range regs {
		out.Registry = append(out.Registry, RegistryConnection{RegistryConnection: r, Apps: regUsage[r.Name]})
	}
	held := map[string]bool{}
	for _, c := range cfs {
		held[c.Name] = true
		out.Cloudflare = append(out.Cloudflare, CloudflareConnection{Name: c.Name, Account: c.Account, Apps: cfUsage[c.Name]})
	}
	for _, name := range slices.Sorted(maps.Keys(cfUsage)) {
		if !held[name] {
			out.Cloudflare = append(out.Cloudflare, CloudflareConnection{Name: name, Apps: cfUsage[name], Missing: true})
		}
	}
	slices.SortFunc(out.Cloudflare, func(a, b CloudflareConnection) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// SaveRegistryConnection defines a registry connection, or gives an existing one a new login.
// Every app that uses it pulls with the new login from then on.
func (o *Ops) SaveRegistryConnection(ctx context.Context, name string, auth cluster.RegistryAuth,
	report progress.Reporter) error {
	rep := progress.OrDiscard(report)
	if err := CheckConnectionName(name); err != nil {
		return err
	}
	if auth.Username == "" || auth.Token == "" {
		return errors.New("a registry connection needs a user name and a token")
	}
	if err := o.Cluster.SaveRegistryConnection(ctx, o.config(), name, auth, rep); err != nil {
		return err
	}
	regUsage, _, err := o.usage(ctx)
	if err != nil {
		return err
	}
	if apps := regUsage[name]; len(apps) > 0 {
		rep.Report(progress.Info("%s pull with it from now on", strings.Join(apps, ", ")))
	}
	return nil
}

// SaveCloudflareConnection checks a Cloudflare token, works out its account, and keeps it on this
// machine as a connection. An existing connection gets the new token; it may not move to another
// account while apps use it, because their tunnels live in the account it has.
func (o *Ops) SaveCloudflareConnection(ctx context.Context, conn hostcfg.Cloudflare, report progress.Reporter) error {
	rep := progress.OrDiscard(report)
	if err := CheckConnectionName(conn.Name); err != nil {
		return err
	}
	conn.Token, conn.Account = strings.TrimSpace(conn.Token), strings.TrimSpace(conn.Account)
	if conn.Token == "" {
		return errors.New("a Cloudflare connection needs an API token")
	}
	api := o.NewCloudflare(conn.Token)
	if err := api.VerifyToken(ctx); err != nil {
		return fmt.Errorf("the Cloudflare token was refused: %w", err)
	}
	if conn.Account == "" {
		account, err := api.AccountID(ctx)
		if err != nil {
			return err
		}
		conn.Account = account
	}
	existing, err := o.Env.Connections.Cloudflare(conn.Name)
	if err != nil {
		return err
	}
	if existing != nil && existing.Account != conn.Account {
		_, cfUsage, err := o.usage(ctx)
		if err != nil {
			return err
		}
		if apps := cfUsage[conn.Name]; len(apps) > 0 {
			return fmt.Errorf("the Cloudflare connection %s is in account %s, and the tunnels of %s live there; "+
				"define a new connection for account %s and move the apps to it", conn.Name, existing.Account,
				strings.Join(apps, ", "), conn.Account)
		}
	}
	if err := o.Env.Connections.SaveCloudflare(conn); err != nil {
		return err
	}
	action := "created"
	if existing != nil {
		action = "updated"
	}
	rep.Report(progress.Applied("Cloudflare connection "+conn.Name, action, "account "+conn.Account))
	return nil
}

// RemoveConnection deletes a connection. It refuses while an app uses it, and names the apps.
func (o *Ops) RemoveConnection(ctx context.Context, kind, name string, report progress.Reporter) error {
	rep := progress.OrDiscard(report)
	if err := CheckConnectionName(name); err != nil {
		return err
	}
	regUsage, cfUsage, err := o.usage(ctx)
	if err != nil {
		return err
	}
	var apps []string
	switch kind {
	case ConnectionRegistry:
		apps = regUsage[name]
	case ConnectionCloudflare:
		apps = cfUsage[name]
	default:
		return fmt.Errorf("a connection is %s or %s, not %q", ConnectionRegistry, ConnectionCloudflare, kind)
	}
	if len(apps) > 0 {
		return fmt.Errorf("the %s connection %s is used by %s; give %s another one first",
			kind, name, strings.Join(apps, ", "), pronoun(len(apps)))
	}
	var found bool
	if kind == ConnectionRegistry {
		found, err = o.Cluster.DeleteRegistryConnection(ctx, o.config(), name, rep)
	} else if found, err = o.Env.Connections.DeleteCloudflare(name); found {
		rep.Report(progress.Applied("Cloudflare connection "+name, "deleted", ""))
	}
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("there is no %s connection %s", kind, name)
	}
	return nil
}

func pronoun(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// Zones returns the zones the Cloudflare connections of this machine can see, for offering them
// as the domain of an app. A connection whose token is refused is left out rather than failing
// the list: it is a suggestion, and the add itself checks the domain.
func (o *Ops) Zones(ctx context.Context) []string {
	conns, err := o.Env.Connections.CloudflareConnections()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, c := range conns {
		zones, err := o.NewCloudflare(c.Token).Zones(ctx)
		if err != nil {
			continue
		}
		for _, z := range zones {
			seen[z.Name] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}
