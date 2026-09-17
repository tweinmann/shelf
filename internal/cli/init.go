package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/tweinmann/shelf/internal/cluster"
)

// DefaultPlatformRepository holds the platform artifact of every shelf release, tagged with the
// release version.
const DefaultPlatformRepository = "oci://ghcr.io/tweinmann/shelf/platform"

// installCluster is replaced in tests.
var installCluster = cluster.Install

// errAborted is returned when the user does not confirm.
var errAborted = errors.New("aborted; nothing was changed")

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up the platform",
	}
	cmd.AddCommand(newInitClusterCmd())
	return cmd
}

func newInitClusterCmd() *cobra.Command {
	var (
		platform   string
		insecure   bool
		kubeconfig string
		kubectx    string
		yes        bool
		timeout    time.Duration
	)
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Install Flux and the platform into the current Kubernetes cluster",
		Long: `Install the Flux Operator and a FluxInstance into the cluster of the current kubecontext.
Flux then installs the platform (Traefik and more) from the platform artifact.

The command shows the target cluster and asks for confirmation, because it installs
cluster-wide objects. It is idempotent; running it again updates what changed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if platform == "" {
				if strings.HasPrefix(version(), "dev") {
					return fmt.Errorf("this is a development build (%s); pass --platform", version())
				}
				platform = DefaultPlatformRepository + ":" + version()
			}
			p, err := cluster.ParsePlatform(platform, insecure)
			if err != nil {
				return err
			}

			rules := clientcmd.NewDefaultClientConfigLoadingRules()
			rules.ExplicitPath = kubeconfig
			overrides := &clientcmd.ConfigOverrides{CurrentContext: kubectx}
			loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
			raw, err := loader.RawConfig()
			if err != nil {
				return fmt.Errorf("reading kubeconfig: %w", err)
			}
			contextName := raw.CurrentContext
			if kubectx != "" {
				contextName = kubectx
			}
			cfg, err := loader.ClientConfig()
			if err != nil {
				return fmt.Errorf("kubeconfig: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Installing Flux %s and the shelf platform into:\n", cluster.FluxVersion)
			fmt.Fprintf(out, "  context   %s\n", contextName)
			fmt.Fprintf(out, "  server    %s\n", cfg.Host)
			fmt.Fprintf(out, "  platform  %s\n", p)
			if !yes {
				ok, err := confirm(cmd.InOrStdin(), out)
				if err != nil {
					return err
				}
				if !ok {
					return errAborted
				}
			}
			return installCluster(cmd.Context(), rest.CopyConfig(cfg), cluster.Options{
				Platform: p,
				Timeout:  timeout,
				Out:      out,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&platform, "platform", "",
		"platform artifact, oci://<registry>/<repository>:<tag> (default: "+DefaultPlatformRepository+":<shelf version>)")
	f.BoolVar(&insecure, "insecure-registry", false, "pull the platform artifact without TLS (dev registry)")
	f.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
	f.StringVar(&kubectx, "context", "", "kubeconfig context (default: the current context)")
	f.BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	f.DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for everything to become ready")
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
