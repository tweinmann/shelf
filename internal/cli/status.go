package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
)

func newAppStatusCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "status [name]",
		Short: "Show how the apps of this cluster are doing",
		Long: `Without a name, list every app with its state. With a name, walk the chain from the
deploy artifact to the running pods and say which step is not ready.

This is what the admin UI shows on its pages, in text.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				return listApps(cmd, shelf, out)
			}
			name := args[0]
			if err := ops.CheckAppName(name); err != nil {
				return err
			}
			app, err := shelf.App(cmd.Context(), name)
			if err != nil {
				return err
			}
			diagnosis, err := shelf.Diagnose(cmd.Context(), name)
			if err != nil {
				return err
			}
			printApp(out, app)
			printChain(out, diagnosis)
			return nil
		},
	}
	target.registerTarget(cmd.Flags())
	return cmd
}

func listApps(cmd *cobra.Command, shelf *ops.Ops, out io.Writer) error {
	apps, err := shelf.Apps(cmd.Context())
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		fmt.Fprintln(out, "No apps yet.")
		return nil
	}
	for _, app := range apps {
		fmt.Fprintf(out, "%s: %s\n", app.Name, app.Phase)
		if app.Reason != "" {
			fmt.Fprintf(out, "  %s: %s\n", app.Stage, firstLine(app.Reason))
		}
	}
	return nil
}

func printApp(out io.Writer, app ops.App) {
	fmt.Fprintf(out, "%s: %s\n", app.Name, app.Phase)
	if app.Host != "" {
		address := app.Host
		if app.URL != "" {
			address = app.URL
		}
		fmt.Fprintf(out, "  address   %s\n", address)
	}
	fmt.Fprintf(out, "  artifact  %s\n", app.Artifact)
	if app.Revision != "" {
		fmt.Fprintf(out, "  running   %s\n", app.Revision)
	}
	if !app.Deployed.IsZero() {
		fmt.Fprintf(out, "  deployed  %s\n", app.Deployed.Local().Format(time.RFC3339))
	}
}

func printChain(out io.Writer, d cluster.Diagnosis) {
	for _, stage := range d.Stages {
		fmt.Fprintf(out, "  %-9s %s\n", stage.Phase, stage.Name)
		if stage.Message != "" {
			fmt.Fprintf(out, "            %s\n", firstLine(stage.Message))
		}
		if stage.Hint != "" && stage.Phase != cluster.PhaseReady {
			fmt.Fprintf(out, "            %s\n", stage.Hint)
		}
	}
}

// firstLine keeps a controller message to one line; the whole of it is in the object.
func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i] + " …"
		}
	}
	return s
}
