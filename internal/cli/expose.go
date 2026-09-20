package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
)

func newInitExposeCmd(o Options) *cobra.Command {
	var (
		tunnelName string
		target     clusterFlags
	)
	cmd := &cobra.Command{
		Use:   "expose",
		Short: "Make the apps of this cluster reachable from the internet",
		Long: `Connect the cluster to Cloudflare: find or create a tunnel, run cloudflared with a single
rule that forwards everything to Traefik, and publish one DNS record per app that already runs.
Apps added later get their record from ` + "`shelf app add`" + `.

The Cloudflare API token comes from ` + ops.EnvCloudflareToken + `; it needs Account:Cloudflare
Tunnel:Edit and Zone:DNS:Edit for the zone. With several accounts, set ` + ops.EnvCloudflareAccount + `.

Requires ` + "`shelf init cluster`" + `, whose domain and host suffix decide the names the apps get.
It is idempotent: an existing tunnel of the same name is reused as long as the cluster still
holds its credentials.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			if shelf.Env.CloudflareToken == "" {
				return fmt.Errorf("%s is not set; it needs Account:Cloudflare Tunnel:Edit and Zone:DNS:Edit",
					ops.EnvCloudflareToken)
			}
			out := cmd.OutOrStdout()
			plan, err := shelf.PlanExpose(cmd.Context(), ops.ExposeOptions{
				TunnelName: tunnelName,
				Timeout:    target.timeout,
			})
			if err != nil {
				return err
			}

			fmt.Fprintln(out, "Exposing the apps of:")
			printTarget(out, shelf.Target)
			fmt.Fprintf(out, "  hosts     %s\n", plan.Hosts)
			fmt.Fprintf(out, "  tunnel    %s\n", plan.Name)
			if plan.Action != "" {
				fmt.Fprintf(out, "  tunnel %s %s\n", plan.Name, plan.Action)
			}
			if plan.NeedsReplacement {
				fmt.Fprintf(out, "  tunnel %s exists, but this cluster does not hold its credentials.\n", plan.Name)
				fmt.Fprintln(out, "  Its secret cannot be read again, so it has to be replaced.")
				if !target.yes {
					ok, err := confirm(cmd.InOrStdin(), out)
					if err != nil {
						return err
					}
					if !ok {
						return errAborted
					}
				}
				if err := shelf.ReplaceTunnel(cmd.Context(), plan); err != nil {
					return err
				}
				fmt.Fprintf(out, "  tunnel %s %s\n", plan.Name, plan.Action)
			}
			fmt.Fprintf(out, "  target    %s\n", plan.Target())
			if !target.yes {
				ok, err := confirm(cmd.InOrStdin(), out)
				if err != nil {
					return err
				}
				if !ok {
					return errAborted
				}
			}
			return shelf.Expose(cmd.Context(), plan, progress.Writer(out))
		},
	}
	f := cmd.Flags()
	f.StringVar(&tunnelName, "tunnel", "", "name of the Cloudflare tunnel (default: shelf<host suffix>)")
	target.register(f)
	return cmd
}
