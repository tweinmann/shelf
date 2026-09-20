package ops

import (
	"context"
	"fmt"
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
