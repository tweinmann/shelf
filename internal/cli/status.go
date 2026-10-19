package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
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
			printComponents(out, app.Components, app.Public || app.Quick)
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
	if app.Registry != "" {
		fmt.Fprintf(out, "  registry  connection %s\n", app.Registry)
	}
	switch {
	case app.CloudflareMissing:
		fmt.Fprintf(out, "  exposed   through connection %s, which this machine does not hold\n", app.Cloudflare)
	case app.Cloudflare != "":
		fmt.Fprintf(out, "  exposed   through connection %s, account %s\n", app.Cloudflare, app.CloudflareAccount)
	case app.Quick && app.URL == "":
		fmt.Fprintln(out, "  exposed   through a quick tunnel, which has no address yet")
	case app.Quick:
		fmt.Fprintln(out, "  exposed   through a quick tunnel; the address changes when it restarts")
	}
	if app.Revision != "" {
		fmt.Fprintf(out, "  running   %s\n", app.Revision)
	}
	if !app.Deployed.IsZero() {
		fmt.Fprintf(out, "  deployed  %s\n", app.Deployed.Local().Format(time.RFC3339))
	}
}

// printComponents lists what the app is made of and where each part answers, the same three
// cases the admin UI shows on the app's page.
func printComponents(out io.Writer, comps []ops.Component, public bool) {
	if len(comps) == 0 {
		return
	}
	width := 0
	for _, c := range comps {
		width = max(width, len(c.Name))
	}
	fmt.Fprintln(out, "  components")
	for _, c := range comps {
		fmt.Fprintf(out, "    %-*s  %-7s  %s\n", width, c.Name, c.Phase, componentAddress(c, public))
	}
}

// componentAddress is the link a browser can follow, or the address plus why it is not one.
func componentAddress(c ops.Component, public bool) string {
	if c.URL != "" {
		return c.URL
	}
	note := "inside the cluster only"
	if !c.Internal && public {
		note = "not exposed"
	}
	if c.Address == "" {
		return note
	}
	return c.Address + " (" + note + ")"
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

func newAppRedeployCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "redeploy <name>",
		Short: "Fetch the app's artifact again and roll out what it finds",
		Long: `Ask Flux to read the deploy artifact again under the tag the app is registered with, and
to roll out what it finds. Nothing about the app changes: this is for a tag that was moved, and
for something that failed and is worth another try.

To move an app to a different tag, run ` + "`shelf app add`" + ` with that reference instead.`,
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
			fmt.Fprintf(out, "Deploying %s again from:\n", name)
			printTarget(out, shelf.Target)
			return shelf.Redeploy(cmd.Context(), name, target.timeout, progress.Writer(out))
		},
	}
	target.register(cmd.Flags())
	return cmd
}

func newAppSecretsCmd(o Options) *cobra.Command {
	var (
		reveal bool
		target clusterFlags
	)
	cmd := &cobra.Command{
		Use:   "secrets <name>",
		Short: "List the generated secrets of an app",
		Long: `List the names of the secrets shelf generated for an app. The values are printed only with
--reveal, because they end up in the terminal and in its history.

shelf generates these values, so this and the admin UI are the only ways to read them — a
database client needs the same password the app gets from its environment.`,
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
			values, err := shelf.Secrets(cmd.Context(), name)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(values) == 0 {
				fmt.Fprintf(out, "%s has no secrets.\n", name)
				return nil
			}
			for _, key := range slices.Sorted(maps.Keys(values)) {
				if reveal {
					fmt.Fprintf(out, "%s: %s\n", key, values[key])
					continue
				}
				fmt.Fprintln(out, key)
			}
			if !reveal {
				fmt.Fprintln(cmd.ErrOrStderr(), "\nPass --reveal to print the values.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "print the values, not only the names")
	target.registerTarget(cmd.Flags())
	return cmd
}
