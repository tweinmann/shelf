package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
)

// Release artifacts. Every shelf release publishes the platform tagged with its version and
// the chart with the version without the leading "v".
const (
	DefaultPlatformRepository = "oci://ghcr.io/tweinmann/shelf/platform"
	DefaultChartRepository    = "oci://ghcr.io/tweinmann/shelf/charts/shelf-app"
)

// Environment variables with the registry login for shelf init cluster. The token is never
// taken from a flag, so it does not show up in process listings or shell history.
const (
	envRegistryUser  = "GHCR_USERNAME"
	envRegistryToken = "GHCR_TOKEN"
)

// errAborted is returned when the user does not confirm.
var errAborted = errors.New("aborted; nothing was changed")

// clusterFlags select the target cluster.
type clusterFlags struct {
	kubeconfig string
	context    string
	yes        bool
	timeout    time.Duration
}

func (f *clusterFlags) register(fs *pflag.FlagSet) {
	fs.StringVar(&f.kubeconfig, "kubeconfig", "", "kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
	fs.StringVar(&f.context, "context", "", "kubeconfig context (default: the current context)")
	fs.BoolVarP(&f.yes, "yes", "y", false, "do not ask for confirmation")
	fs.DurationVar(&f.timeout, "timeout", 5*time.Minute, "how long to wait for everything to become ready")
}

// load turns the flags into the operations against that cluster.
func (f *clusterFlags) load(o Options) (*ops.Ops, error) {
	env, err := o.env()
	if err != nil {
		return nil, err
	}
	t, err := ops.LoadTarget(f.kubeconfig, f.context)
	if err != nil {
		return nil, err
	}
	return o.newOps(t, env), nil
}

// printTarget shows the target cluster, so a wrong kubecontext is noticed before anything
// changes.
func printTarget(w io.Writer, t ops.Target) {
	fmt.Fprintf(w, "  context   %s\n", t.Context)
	fmt.Fprintf(w, "  server    %s\n", t.Config.Host)
}

func newInitCmd(o Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up the platform",
	}
	cmd.AddCommand(newInitClusterCmd(o), newInitExposeCmd(o))
	return cmd
}

// releaseArtifact returns ref, or repository:<tag> for a release build.
func (o Options) releaseArtifact(ref, repository, tag, flag string) (cluster.Artifact, error) {
	if ref == "" {
		if version := o.version(); strings.HasPrefix(version, "dev") {
			return cluster.Artifact{}, fmt.Errorf("this is a development build (%s); pass --%s", version, flag)
		}
		ref = repository + ":" + tag
	}
	a, err := cluster.ParseArtifact(ref)
	if err != nil {
		return cluster.Artifact{}, fmt.Errorf("--%s: %w", flag, err)
	}
	return a, nil
}

// registryAuth reads the login `shelf init cluster` stores in the cluster.
func (o Options) registryAuth() (*cluster.RegistryAuth, error) {
	user, token := o.getenv(envRegistryUser), o.getenv(envRegistryToken)
	switch {
	case token == "":
		return nil, nil
	case user == "":
		return nil, fmt.Errorf("%s is set, so %s is needed too", envRegistryToken, envRegistryUser)
	}
	return &cluster.RegistryAuth{Username: user, Token: token}, nil
}

func newInitClusterCmd(o Options) *cobra.Command {
	var (
		platform   string
		chart      string
		domain     string
		hostSuffix string
		insecure   bool
		target     clusterFlags
	)
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Install Flux and the platform into the current Kubernetes cluster",
		Long: `Install the Flux Operator and a FluxInstance into the cluster of the current kubecontext.
Flux then installs the platform (Traefik and the app machinery) from the platform artifact.

The registry login for private images and deploy artifacts comes from ` + envRegistryUser + ` and
` + envRegistryToken + ` (a classic PAT with read:packages). Without them, a stored login is kept.

The command shows the target cluster and asks for confirmation, because it installs
cluster-wide objects. It is idempotent; running it again updates what changed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			version := o.version()
			p, err := o.releaseArtifact(platform, DefaultPlatformRepository, version, "platform")
			if err != nil {
				return err
			}
			c, err := o.releaseArtifact(chart, DefaultChartRepository, strings.TrimPrefix(version, "v"), "chart")
			if err != nil {
				return err
			}
			if err := ops.CheckDomain(domain); err != nil {
				return fmt.Errorf("--domain %w", err)
			}
			if err := ops.CheckHostSuffix(hostSuffix); err != nil {
				return fmt.Errorf("--host-suffix %w", err)
			}
			auth, err := o.registryAuth()
			if err != nil {
				return err
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			shelf.Env.Registry = auth

			opts := ops.InitOptions{
				Platform:   p,
				Chart:      c,
				Domain:     domain,
				HostSuffix: hostSuffix,
				Insecure:   insecure,
				Timeout:    target.timeout,
			}
			plan := shelf.PlanCluster(opts)
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Installing Flux %s and the shelf platform into:\n", cluster.FluxVersion)
			printTarget(out, shelf.Target)
			fmt.Fprintf(out, "  platform  %s\n", plan.Platform)
			fmt.Fprintf(out, "  chart     %s\n", plan.Chart)
			fmt.Fprintf(out, "  hosts     %s\n", plan.Hosts)
			if plan.Registry != "" {
				fmt.Fprintf(out, "  registry  %s\n", plan.Registry)
			} else {
				fmt.Fprintf(out, "  registry  login unchanged (%s not set)\n", envRegistryToken)
			}
			if !target.yes {
				ok, err := confirm(cmd.InOrStdin(), out)
				if err != nil {
					return err
				}
				if !ok {
					return errAborted
				}
			}
			return shelf.InitCluster(cmd.Context(), opts, progress.Writer(out))
		},
	}
	f := cmd.Flags()
	f.StringVar(&platform, "platform", "",
		"platform artifact, oci://<registry>/<repository>:<tag> (default: "+DefaultPlatformRepository+":<shelf version>)")
	f.StringVar(&chart, "chart", "",
		"shelf-app chart, oci://<registry>/<repository>:<version> (default: "+DefaultChartRepository+":<shelf version>)")
	f.StringVar(&domain, "domain", "", "apps are reachable at <app><host-suffix>.<domain> (required)")
	f.StringVar(&hostSuffix, "host-suffix", "",
		"suffix in the app's host name, e.g. -dev, to separate clusters that share a DNS zone")
	f.BoolVar(&insecure, "insecure-registry", false, "pull platform and chart without TLS (dev registry)")
	target.register(f)
	return cmd
}

// confirm asks on out and reads the answer from in; only y or yes proceeds.
func confirm(in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "Proceed? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	if errors.Is(err, io.EOF) {
		fmt.Fprintln(out)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
