package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/tweinmann/shelf/internal/cluster"
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

// installCluster is replaced in tests.
var installCluster = cluster.Install

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

// target is a loaded kubeconfig context.
type target struct {
	context string
	config  *rest.Config
}

func (f *clusterFlags) load() (*target, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = f.kubeconfig
	overrides := &clientcmd.ConfigOverrides{CurrentContext: f.context}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	raw, err := loader.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig: %w", err)
	}
	name := raw.CurrentContext
	if f.context != "" {
		name = f.context
	}
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %w", err)
	}
	return &target{context: name, config: rest.CopyConfig(cfg)}, nil
}

// print shows the target cluster, so a wrong kubecontext is noticed before anything changes.
func (t *target) print(w io.Writer) {
	fmt.Fprintf(w, "  context   %s\n", t.context)
	fmt.Fprintf(w, "  server    %s\n", t.config.Host)
}

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up the platform",
	}
	cmd.AddCommand(newInitClusterCmd(), newInitExposeCmd())
	return cmd
}

// releaseArtifact returns ref, or repository:<tag> for a release build.
func releaseArtifact(ref, repository, tag, flag string) (cluster.Artifact, error) {
	if ref == "" {
		if strings.HasPrefix(version(), "dev") {
			return cluster.Artifact{}, fmt.Errorf("this is a development build (%s); pass --%s", version(), flag)
		}
		ref = repository + ":" + tag
	}
	a, err := cluster.ParseArtifact(ref)
	if err != nil {
		return cluster.Artifact{}, fmt.Errorf("--%s: %w", flag, err)
	}
	return a, nil
}

func registryAuth() (*cluster.RegistryAuth, error) {
	user, token := os.Getenv(envRegistryUser), os.Getenv(envRegistryToken)
	switch {
	case token == "":
		return nil, nil
	case user == "":
		return nil, fmt.Errorf("%s is set, so %s is needed too", envRegistryToken, envRegistryUser)
	}
	return &cluster.RegistryAuth{Username: user, Token: token}, nil
}

func newInitClusterCmd() *cobra.Command {
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
			p, err := releaseArtifact(platform, DefaultPlatformRepository, version(), "platform")
			if err != nil {
				return err
			}
			c, err := releaseArtifact(chart, DefaultChartRepository, strings.TrimPrefix(version(), "v"), "chart")
			if err != nil {
				return err
			}
			if domain == "" || len(k8svalidation.IsDNS1123Subdomain(domain)) > 0 {
				return fmt.Errorf("--domain must be a DNS name such as example.com")
			}
			// The suffix becomes part of a DNS label: <app><suffix>.<domain>.
			if hostSuffix != "" && len(k8svalidation.IsDNS1123Label("a"+hostSuffix)) > 0 {
				return fmt.Errorf("--host-suffix %q must fit into a host name, such as -dev", hostSuffix)
			}
			auth, err := registryAuth()
			if err != nil {
				return err
			}
			t, err := target.load()
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Installing Flux %s and the shelf platform into:\n", cluster.FluxVersion)
			t.print(out)
			fmt.Fprintf(out, "  platform  %s\n", p)
			fmt.Fprintf(out, "  chart     %s\n", c)
			fmt.Fprintf(out, "  hosts     %s\n", "<app>"+hostSuffix+"."+domain)
			if auth != nil {
				fmt.Fprintf(out, "  registry  %s as %s\n", cluster.RegistryHost, auth.Username)
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
			return installCluster(cmd.Context(), t.config, cluster.Options{
				Platform: p,
				// The tunnel target is written by `shelf init expose` and kept as it is here.
				Settings: cluster.Settings{
					Domain:           domain,
					HostSuffix:       hostSuffix,
					Chart:            c,
					InsecureRegistry: insecure,
				},
				Registry: auth,
				Timeout:  target.timeout,
				Out:      out,
			})
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
