package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/progress"
)

// Environment variables with the credentials of a connection. A token is never taken from a
// flag, so it does not show up in process listings or shell history.
const (
	envRegistryUser      = "GHCR_USERNAME"
	envRegistryToken     = "GHCR_TOKEN"
	envCloudflareToken   = "CF_API_TOKEN"
	envCloudflareAccount = "CF_ACCOUNT_ID"
)

func newConnectionCmd(o Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connection",
		Short: "Define the registry and Cloudflare connections apps use",
		Long: `A connection is a login defined once, by name, and assigned to any number of apps with
` + "`shelf app add`" + ` or ` + "`shelf app credentials`" + `.

A registry connection is a login for ghcr.io. It lives in the cluster, which pulls with it, and a
new token reaches every app that uses it at once. A Cloudflare connection is an API token and
the account the apps' tunnels are made in. It lives in $SHELF_HOME/connections on this machine
and never goes into the cluster, so only this machine can expose apps through it.`,
	}
	add := &cobra.Command{Use: "add", Short: "Define a connection, or give one a new token"}
	add.AddCommand(newConnectionAddRegistryCmd(o), newConnectionAddCloudflareCmd(o))
	cmd.AddCommand(add, newConnectionListCmd(o), newConnectionRmCmd(o),
		newConnectionPackagesCmd(o), newConnectionZonesCmd(o))
	return cmd
}

func newConnectionAddRegistryCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "registry <name>",
		Short: "Define a registry connection from " + envRegistryUser + " and " + envRegistryToken,
		Long: `Define a registry connection for ghcr.io from ` + envRegistryUser + ` and ` + envRegistryToken + ` (a classic
PAT with read:packages; a fine-grained token cannot pull). With the name of an existing
connection, it gets the new login, and every app that uses it pulls with that from then on.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			user, token := o.getenv(envRegistryUser), o.getenv(envRegistryToken)
			if user == "" || token == "" {
				return fmt.Errorf("a registry connection needs %s and %s", envRegistryUser, envRegistryToken)
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			return shelf.SaveRegistryConnection(cmd.Context(), args[0],
				cluster.RegistryAuth{Username: user, Token: token}, progress.Writer(cmd.OutOrStdout()))
		},
	}
	target.registerTarget(cmd.Flags())
	return cmd
}

func newConnectionAddCloudflareCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "cloudflare <name>",
		Short: "Define a Cloudflare connection from " + envCloudflareToken,
		Long: `Define a Cloudflare connection from ` + envCloudflareToken + `, and ` + envCloudflareAccount + ` when the token sees
several accounts. The token needs Account:Cloudflare Tunnel:Edit, and Zone:DNS:Edit for the
zones of the apps. shelf checks it before keeping it. With the name of an existing connection,
it gets the new token; it cannot move to another account while apps are exposed through it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			token := strings.TrimSpace(o.getenv(envCloudflareToken))
			if token == "" {
				return fmt.Errorf("a Cloudflare connection needs %s", envCloudflareToken)
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			return shelf.SaveCloudflareConnection(cmd.Context(), hostcfg.Cloudflare{
				Name: args[0], Token: token, Account: o.getenv(envCloudflareAccount),
			}, progress.Writer(cmd.OutOrStdout()))
		},
	}
	target.registerTarget(cmd.Flags())
	return cmd
}

func newConnectionListCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the connections and the apps that use them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			conns, err := shelf.Connections(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(conns.Registry)+len(conns.Cloudflare) == 0 {
				fmt.Fprintln(out, "No connections yet.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			for _, c := range conns.Registry {
				fmt.Fprintf(tw, "registry\t%s\t%s@%s\t%s\n", c.Name, c.Username, cluster.RegistryHost, usedBy(c.Apps))
			}
			for _, c := range conns.Cloudflare {
				account := "account " + c.Account
				if c.Missing {
					account = "not on this machine"
				}
				fmt.Fprintf(tw, "cloudflare\t%s\t%s\t%s\n", c.Name, account, usedBy(c.Apps))
			}
			return tw.Flush()
		},
	}
	target.registerTarget(cmd.Flags())
	return cmd
}

func usedBy(apps []string) string {
	if len(apps) == 0 {
		return "unused"
	}
	return "used by " + strings.Join(apps, ", ")
}

func newConnectionRmCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "rm registry|cloudflare <name>",
		Short: "Remove a connection no app uses",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			return shelf.RemoveConnection(cmd.Context(), args[0], args[1], progress.Writer(cmd.OutOrStdout()))
		},
	}
	target.registerTarget(cmd.Flags())
	return cmd
}
