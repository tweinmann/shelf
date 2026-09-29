package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/ops"
)

// The commands in this file list what the admin UI offers as choices: the deploy artifacts a
// registry connection can pull, their tags, and the zones of a Cloudflare connection.

const listTime = "2006-01-02 15:04"

func newConnectionPackagesCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "packages <registry-connection>",
		Short: "List the deploy artifacts a registry connection can pull",
		Long: `List the deploy artifacts of the connection's GitHub user, read from the GitHub API with the
connection's token. The images the workflow builds for the components are left out.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			pkgs, err := shelf.Packages(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(pkgs) == 0 {
				fmt.Fprintln(out, "No deploy artifacts.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			for _, p := range pkgs {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.Artifact, stamp(p.Updated))
			}
			return tw.Flush()
		},
	}
	target.registerTarget(cmd.Flags())
	return cmd
}

func newConnectionZonesCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "zones <cloudflare-connection>",
		Short: "List the zones in the account of a Cloudflare connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			zones, err := shelf.Zones(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(zones) == 0 {
				fmt.Fprintln(out, "No zones.")
			}
			for _, z := range zones {
				fmt.Fprintln(out, z)
			}
			return nil
		},
	}
	target.registerTarget(cmd.Flags())
	return cmd
}

func newAppTagsCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "tags <name>",
		Short: "List the tags of an app's deploy artifact, newest first",
		Long: `List the tags of the deploy artifact an app runs, newest first, read from the GitHub API with
the app's registry connection. The tag the app is on is marked with *. Move the app to another
one with ` + "`shelf app add <name> <artifact>:<tag>`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ops.CheckAppName(args[0]); err != nil {
				return err
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			app, err := shelf.App(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			tags, err := shelf.AppTags(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(tags) == 0 {
				fmt.Fprintln(out, "No tags.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			for _, t := range tags {
				mark := " "
				if t.Name == app.Artifact.Tag {
					mark = "*"
				}
				fmt.Fprintf(tw, "%s %s\t%s\n", mark, t.Name, stamp(t.Created))
			}
			return tw.Flush()
		},
	}
	target.registerTarget(cmd.Flags())
	return cmd
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(listTime)
}
