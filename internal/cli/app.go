package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
)

func newAppCmd(o Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Add and remove apps",
	}
	cmd.AddCommand(newAppAddCmd(o), newAppRmCmd(o))
	return cmd
}

func newAppAddCmd(o Options) *cobra.Command {
	var (
		insecure bool
		target   clusterFlags
	)
	cmd := &cobra.Command{
		Use:   "add <name> <oci://registry/repository:tag>",
		Short: "Deploy an app from its deploy artifact, and keep it updated",
		Long: `Add an app to the platform. From then on, Flux deploys every new version of the deploy
artifact under the given tag, usually "main".

shelf reads the artifact (registry credentials come from the Docker config), generates the
secrets the app declares and stores them in the cluster and in a backup file on this machine
(` + "$SHELF_HOME/apps/<name>/secrets.yaml, default ~/.shelf" + `). If the cluster was rebuilt, the
backup restores the old values. Running add again updates the artifact reference, generates
secrets that were added to app.yaml since, and keeps all existing values.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ops.CheckAppName(name); err != nil {
				return err
			}
			artifact, err := cluster.ParseArtifact(args[1])
			if err != nil {
				return err
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Adding app %s from %s to:\n", name, artifact)
			printTarget(out, shelf.Target)

			return shelf.AddApp(cmd.Context(), ops.AddOptions{
				Name:     name,
				Artifact: artifact,
				Insecure: insecure,
				Timeout:  target.timeout,
			}, progress.Writer(out))
		},
	}
	cmd.Flags().BoolVar(&insecure, "insecure-registry", false, "pull the deploy artifact without TLS (dev registry)")
	target.register(cmd.Flags())
	return cmd
}

func newAppRmCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an app with all its data",
		Long: `Remove an app: its namespace with all objects and volumes, and its secrets in the
cluster. The secret backup on this machine is kept.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ops.CheckAppName(name); err != nil {
				return err
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Removing app %s, including all its volumes, from:\n", name)
			printTarget(out, shelf.Target)
			if !target.yes {
				ok, err := confirm(cmd.InOrStdin(), out)
				if err != nil {
					return err
				}
				if !ok {
					return errAborted
				}
			}
			return shelf.RemoveApp(cmd.Context(), name, target.timeout, progress.Writer(out))
		},
	}
	target.register(cmd.Flags())
	return cmd
}
