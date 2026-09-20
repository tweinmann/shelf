package ops

import (
	"context"
	"fmt"
	"strings"
	"time"

	k8svalidation "k8s.io/apimachinery/pkg/util/validation"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/progress"
)

// InitOptions configure InitCluster.
type InitOptions struct {
	Platform cluster.Artifact
	Chart    cluster.Artifact
	// Domain and HostSuffix decide the host name of every app: <app><suffix>.<domain>.
	Domain     string
	HostSuffix string
	// Insecure allows a platform and chart registry without TLS.
	Insecure bool
	Timeout  time.Duration
}

// InitPlan is what an installation would do, for showing it before it happens.
type InitPlan struct {
	Platform cluster.Artifact
	Chart    cluster.Artifact
	// Hosts is the pattern the apps answer under, such as <app>-dev.example.com.
	Hosts string
	// Registry is the login that will be stored, such as "ghcr.io as tobi". It is empty when
	// the login already in the cluster is kept.
	Registry string
}

// CheckDomain rejects a domain that cannot carry host names. The message has no leading field
// name, so a caller can put its own in front: a flag on the command line, a form field in the
// admin UI.
func CheckDomain(domain string) error {
	if domain == "" || len(k8svalidation.IsDNS1123Subdomain(domain)) > 0 {
		return fmt.Errorf("must be a DNS name such as example.com")
	}
	return nil
}

// CheckHostSuffix rejects a suffix that would not fit into a host name. The suffix becomes part
// of a DNS label: <app><suffix>.<domain>.
func CheckHostSuffix(suffix string) error {
	if suffix != "" && len(k8svalidation.IsDNS1123Label("a"+suffix)) > 0 {
		return fmt.Errorf("%q must fit into a host name, such as -dev", suffix)
	}
	return nil
}

// Hosts is the pattern the apps of a cluster answer under.
func Hosts(domain, suffix string) string { return "<app>" + suffix + "." + domain }

// reservedSuffixes are the names that cannot exist on the internet: RFC 6762 keeps .local for
// multicast DNS, RFC 6761 and RFC 8375 reserve the rest for local use. A name under them can
// never be delegated to a DNS provider, so no record can point at a tunnel.
var reservedSuffixes = []string{
	".local", ".localhost", ".internal", ".test", ".invalid", ".example", ".home.arpa",
}

// PublicDomain reports whether a domain can carry host names that a browser anywhere resolves.
// The development cluster uses dev.local, which cannot, and shelf must not offer a link to a
// name that will never answer.
func PublicDomain(domain string) bool {
	if domain == "" || !strings.Contains(domain, ".") {
		return false
	}
	name := strings.ToLower(strings.TrimSuffix(domain, "."))
	for _, suffix := range reservedSuffixes {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
}

// PlanCluster describes what InitCluster would install.
func (o *Ops) PlanCluster(opts InitOptions) InitPlan {
	plan := InitPlan{
		Platform: opts.Platform,
		Chart:    opts.Chart,
		Hosts:    Hosts(opts.Domain, opts.HostSuffix),
	}
	if o.Env.Registry != nil {
		plan.Registry = cluster.RegistryHost + " as " + o.Env.Registry.Username
	}
	return plan
}

// InitCluster installs Flux and the platform and waits until everything is ready.
func (o *Ops) InitCluster(ctx context.Context, opts InitOptions, rep progress.Reporter) error {
	return o.Cluster.Install(ctx, o.config(), cluster.Options{
		Platform: opts.Platform,
		// The tunnel target is written by Expose and kept as it is here.
		Settings: cluster.Settings{
			Domain:           opts.Domain,
			HostSuffix:       opts.HostSuffix,
			Chart:            opts.Chart,
			InsecureRegistry: opts.Insecure,
		},
		Registry: o.Env.Registry,
		Timeout:  opts.Timeout,
		Report:   rep,
	})
}
