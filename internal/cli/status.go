package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
)

func newAppStatusCmd(o Options) *cobra.Command {
	var (
		target  clusterFlags
		asJSON  bool
		wait    bool
		commit  string
		timeout time.Duration
		preview string
	)
	cmd := &cobra.Command{
		Use:   "status [name]",
		Short: "Show how the apps of this cluster are doing",
		Long: `Without a name, list every app with its state. With a name, walk the chain from the
deploy artifact to the running pods and say which step is not ready.

With --wait, wait until the app is Ready or a step failed, and print the verdict. With --commit
as well, wait first until the app runs the deploy artifact built from that git commit, so that
the verdict is about it and not about the version before. The exit status is 0 when the app is
Ready, 1 when a step failed and 2 when the time ran out. Waiting only reads the cluster; the
platform notices a new artifact within a minute by itself.

With --preview, all of this is about the preview of that branch instead of the app: its
namespace <app>--<slug>, its artifact at the branch tag, and its stages.

This is what the admin UI shows on its pages, in text.`,
		Example: `  shelf app status hello --wait --commit "$(git rev-parse HEAD)" --json
  shelf app status hello --preview feature/login --wait --commit "$(git rev-parse HEAD)" --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commit != "" && !wait {
				return errors.New("--commit needs --wait")
			}
			if wait && len(args) == 0 {
				return errors.New("--wait needs the name of an app")
			}
			if preview != "" && len(args) == 0 {
				return errors.New("--preview needs the name of an app")
			}
			if commit != "" {
				if err := ops.CheckCommit(commit); err != nil {
					return err
				}
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				return listApps(cmd, shelf, out, asJSON)
			}
			name := args[0]
			if err := ops.CheckAppName(name); err != nil {
				return err
			}
			if wait {
				result, err := shelf.Wait(cmd.Context(), name,
					ops.WaitOptions{Commit: commit, Timeout: timeout, Preview: preview})
				if err != nil {
					return err
				}
				if asJSON {
					err = printJSON(out, waitResult(result))
				} else {
					printVerdict(out, result)
				}
				if err != nil {
					return err
				}
				return verdictExit(result.Outcome)
			}
			var app ops.App
			if preview != "" {
				app, err = shelf.Preview(cmd.Context(), name, preview)
			} else {
				app, err = shelf.App(cmd.Context(), name)
			}
			if err != nil {
				return err
			}
			diagnosis, err := shelf.Diagnose(cmd.Context(), app.Name)
			if err != nil {
				return err
			}
			if asJSON {
				doc := appStatus(app, diagnosis)
				if preview != "" {
					doc.PreviewOf = name
				}
				return printJSON(out, doc)
			}
			printApp(out, app)
			printComponents(out, app.Components, app.Public || app.Quick)
			printVerification(out, app.Verification, preview != "")
			printIsolation(out, app.Isolation)
			printChain(out, diagnosis)
			printPreviews(out, app)
			return nil
		},
	}
	target.registerTarget(cmd.Flags())
	registerJSON(cmd, &asJSON)
	cmd.Flags().BoolVar(&wait, "wait", false, "wait until the app is Ready or a step failed, and print the verdict")
	cmd.Flags().StringVar(&commit, "commit", "", "with --wait: the git commit the app has to run, at least 7 hex digits")
	cmd.Flags().DurationVar(&timeout, "timeout", ops.DefaultWaitTimeout, "with --wait: how long to wait")
	cmd.Flags().StringVar(&preview, "preview", "", "the preview of this branch (its name or its slug) instead of the app")
	return cmd
}

// printVerdict is the wait's result in three lines at most: the outcome, the step that is not
// Ready with its reason, and shelf's hint.
func printVerdict(out io.Writer, r ops.WaitResult) {
	line := fmt.Sprintf("%s: %s", r.App, r.Outcome)
	if r.Commit != "" {
		line += ", running commit " + r.Commit[:min(len(r.Commit), 12)]
	}
	fmt.Fprintln(out, line)
	if r.Stage == nil {
		return
	}
	detail := r.Stage.Name
	for _, part := range []string{r.Stage.Reason, firstLine(r.Stage.Message)} {
		if part != "" {
			detail += ": " + part
		}
	}
	fmt.Fprintf(out, "  %s\n", detail)
	if r.Stage.Hint != "" {
		fmt.Fprintf(out, "  %s\n", r.Stage.Hint)
	}
}

// verdictExit turns an outcome into the command's exit status: 0, 1 or 2.
func verdictExit(outcome ops.Outcome) error {
	switch outcome {
	case ops.OutcomeReady:
		return nil
	case ops.OutcomeFailed:
		return &ExitError{Code: 1}
	default:
		return &ExitError{Code: 2}
	}
}

func listApps(cmd *cobra.Command, shelf *ops.Ops, out io.Writer, asJSON bool) error {
	apps, err := shelf.Apps(cmd.Context())
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(out, appList(apps))
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
		for _, p := range app.Previews {
			fmt.Fprintf(out, "  preview %s: %s\n", p.Slug, p.Phase)
		}
	}
	return nil
}

// printPreviews lists an app's previews, and says when the limit leaves further branches out.
func printPreviews(out io.Writer, app ops.App) {
	if app.PreviewLimit == 0 {
		return
	}
	fmt.Fprintf(out, "  previews  %d of at most %d, inside the cluster only\n", len(app.Previews), app.PreviewLimit)
	for _, p := range app.Previews {
		line := fmt.Sprintf("    %s  %s  %s", p.Name, p.Phase, p.Artifact.Tag)
		if p.Commit != "" {
			line += "  " + p.Commit[:min(len(p.Commit), 12)]
		}
		fmt.Fprintln(out, line)
	}
	if len(app.Previews) >= app.PreviewLimit {
		fmt.Fprintf(out, "    the preview limit of %d is reached: further branches get no preview "+
			"(`shelf app credentials %s --previews=N` raises it)\n", app.PreviewLimit, app.Name)
	}
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
	if app.Commit != "" {
		fmt.Fprintf(out, "  commit    %s\n", app.Commit)
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

// printVerification is the chain from the commit to what runs: where the deploy artifact says it
// comes from, whether the cluster's signer signed it and CI scanned every image, and so whether
// the app may be exposed.
func printVerification(out io.Writer, v *ops.Verification, preview bool) {
	if v == nil {
		return
	}
	if v.Source != "" {
		source := v.Source
		if v.Commit != "" {
			source += " at " + v.Commit[:min(len(v.Commit), 12)]
		}
		fmt.Fprintf(out, "  source    %s\n", source)
	}
	fmt.Fprintf(out, "  signer    %s\n", v.Signer)
	if v.Verified {
		fmt.Fprintln(out, "  verified  yes")
	} else {
		fmt.Fprintf(out, "  verified  no: %s\n", v.Reason)
		fmt.Fprintf(out, "            %s\n", v.Hint)
	}
	if v.Scan != nil {
		fmt.Fprintf(out, "  scan      %s, %s\n", v.Scan.Scanner, v.Scan.ScannedAt)
		width := 0
		for _, img := range v.Scan.Images {
			width = max(width, len(img.Component))
		}
		for _, img := range v.Scan.Images {
			findings := "not scanned"
			if img.Scanned {
				findings = fmt.Sprintf("%d critical, %d high", img.Critical, img.High)
			}
			fmt.Fprintf(out, "    %-*s  %s\n", width, img.Component, findings)
		}
	}
	switch {
	case preview:
		fmt.Fprintln(out, "  exposure  never: a preview stays inside the cluster")
	case v.Exposable:
		fmt.Fprintln(out, "  exposure  allowed")
	default:
		fmt.Fprintln(out, "  exposure  refused until it is verified")
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

// printIsolation says how the app is fenced in: the Pod Security level, the pods the level
// restricted would refuse, and the private ranges it may reach.
func printIsolation(out io.Writer, iso *cluster.Isolation) {
	if iso == nil {
		return
	}
	if iso.PodSecurity == "" {
		fmt.Fprintln(out, "  pod security  none: the platform predates it; `shelf init cluster` installs the current one")
	} else {
		fmt.Fprintf(out, "  pod security  %s\n", iso.PodSecurity)
	}
	for _, v := range iso.Restricted {
		fmt.Fprintf(out, "    restricted would refuse %s: %s\n", v.Pod, v.Reason)
	}
	lan := "none"
	if len(iso.LAN) > 0 {
		lan = strings.Join(iso.LAN, ", ")
	}
	fmt.Fprintf(out, "  lan           %s\n", lan)
}

func printChain(out io.Writer, d cluster.Diagnosis) {
	for _, stage := range d.Stages {
		fmt.Fprintf(out, "  %-9s %s\n", stage.Phase, stage.Name)
		switch {
		// A failed test brings the end of its log, which is the point of showing it.
		case stage.Name == "the tests" && stage.Phase == cluster.PhaseFailed:
			for _, line := range strings.Split(stage.Message, "\n") {
				fmt.Fprintf(out, "            %s\n", line)
			}
		case stage.Message != "":
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
