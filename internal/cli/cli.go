// Package cli implements the shelf command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/render"
	"github.com/tweinmann/shelf/internal/schema"
	"github.com/tweinmann/shelf/internal/validate"
)

// ErrReported means the problem was already printed; main only sets the exit code.
var ErrReported = errors.New("errors reported")

// Version is set at build time with -ldflags "-X github.com/tweinmann/shelf/internal/cli.Version=...".
var Version = ""

// New returns the root command. The resolver is injected so tests can avoid the network.
func New(resolver render.Resolver) *cobra.Command {
	root := &cobra.Command{
		Use:           "shelf",
		Short:         "A small self-hosted app platform",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newValidateCmd(),
		newRenderCmd(resolver),
		newSchemaCmd(),
		newInitCmd(),
		newVersionCmd(),
	)
	return root
}

func readDocument(file string) (*schema.Document, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return schema.Parse(file, data)
}

func printFindings(w io.Writer, file string, findings validate.Findings) {
	for _, f := range findings {
		fmt.Fprintln(w, f.Format(file))
	}
}

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <app.yaml>...",
		Short: "Check app.yaml files without contacting a registry",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			failed := false
			for _, file := range args {
				doc, err := readDocument(file)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "%v\n", err)
					failed = true
					continue
				}
				findings := validate.Validate(doc)
				printFindings(cmd.ErrOrStderr(), file, findings)
				if findings.HasErrors() {
					failed = true
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", file, summarize(findings))
			}
			if failed {
				return ErrReported
			}
			return nil
		},
	}
}

func summarize(findings validate.Findings) string {
	errs, warns := findings.Count(validate.Error), findings.Count(validate.Warning)
	switch {
	case errs > 0:
		return fmt.Sprintf("invalid (%s, %s)", plural(errs, "error"), plural(warns, "warning"))
	case warns > 0:
		return fmt.Sprintf("valid (%s)", plural(warns, "warning"))
	}
	return "valid"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func newRenderCmd(resolver render.Resolver) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "render <app.yaml>",
		Short: "Resolve an app.yaml and print the deploy manifest",
		Long: `Resolve an app.yaml: pin images by digest, substitute ${<component>.host} and port
references, escape literal $ for Kubernetes, and normalize the structure.

The manifest goes to stdout; findings and a summary of what will be created go to stderr.
Secret values never appear in the output; ${secrets.<name>} is resolved in the cluster.
Registry credentials come from the Docker config (docker login).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "configmap" && output != "app" {
				return fmt.Errorf("--output must be configmap or app, got %q", output)
			}
			file := args[0]
			doc, err := readDocument(file)
			if err != nil {
				return err
			}
			app, findings, err := render.Render(cmd.Context(), doc, resolver)
			printFindings(cmd.ErrOrStderr(), file, findings)
			if errors.Is(err, render.ErrInvalid) {
				return ErrReported
			}
			if err != nil {
				return err
			}

			var out []byte
			if output == "app" {
				out, err = render.MarshalApp(app)
			} else {
				out, err = render.ConfigMap(app)
			}
			if err != nil {
				return err
			}
			if _, err := cmd.OutOrStdout().Write(out); err != nil {
				return err
			}
			writeSummary(cmd.ErrOrStderr(), app)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "configmap",
		"what to print: configmap (the deploy manifest) or app (the resolved app.yaml)")
	return cmd
}

// writeSummary lists the objects the app will get, by name.
func writeSummary(w io.Writer, app *schema.App) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "\nApp %s (namespace %s):\n", app.Name, app.Name)
	for _, compName := range slices.Sorted(maps.Keys(app.Components)) {
		comp := app.Components[compName]
		kind := "Deployment"
		if len(comp.Volumes) > 0 {
			kind = "StatefulSet"
		}
		fmt.Fprintf(tw, "  %s\t%s × %d\t%s\n", compName, kind, *comp.Instances, comp.Image)
		if len(comp.Ports) > 0 {
			var ports []string
			for _, n := range slices.Sorted(maps.Keys(comp.Ports)) {
				ports = append(ports, fmt.Sprintf("%s=%d", n, comp.Ports[n]))
			}
			fmt.Fprintf(tw, "  \tService %s\t%s\n", compName, strings.Join(ports, " "))
		}
		if comp.Route != nil {
			fmt.Fprintf(tw, "  \tRoute\t%s → port %s\n", comp.Route.Path, comp.Route.Port)
		}
		for _, volName := range slices.Sorted(maps.Keys(comp.Volumes)) {
			vol := comp.Volumes[volName]
			fmt.Fprintf(tw, "  \tPVC %s\t%s at %s\n",
				pvcNames(volName, compName, *comp.Instances), vol.Size, vol.Path)
		}
	}
	tw.Flush()
	for _, secretName := range slices.Sorted(maps.Keys(app.Secrets)) {
		fmt.Fprintf(w, "  secret %s (generated) → env %s\n", secretName, schema.SecretEnvName(secretName))
	}
}

// pvcNames lists the PVCs a StatefulSet creates: <volume>-<statefulset>-<ordinal>.
func pvcNames(volume, component string, instances int) string {
	names := make([]string, instances)
	for i := range names {
		names[i] = fmt.Sprintf("%s-%s-%d", volume, component, i)
	}
	return strings.Join(names, ", ")
}

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for app.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := schema.JSONSchema()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", s)
			return err
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the shelf version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "shelf %s (app.yaml %s)\n", version(), schema.APIVersion)
		},
	}
}

func version() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 12 {
				return "dev-" + s.Value[:12]
			}
		}
	}
	return "dev"
}

// Execute runs the command line and returns the process exit code.
func Execute(ctx context.Context, cmd *cobra.Command) int {
	if err := cmd.ExecuteContext(ctx); err != nil {
		if !errors.Is(err, ErrReported) {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err)
		}
		return 1
	}
	return 0
}
