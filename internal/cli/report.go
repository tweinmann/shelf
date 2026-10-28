package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
)

func newAppReportCmd(o Options) *cobra.Command {
	var (
		target  clusterFlags
		asJSON  bool
		preview string
	)
	cmd := &cobra.Command{
		Use:   "report <name>",
		Short: "Print a verification report of an app or a preview, as Markdown",
		Long: `Print what a pull request needs to know about the version that runs: the commit, whether it
is Ready or which step is not, its tests, its signature and scan, and how it is fenced in.

With --preview, the report is about the preview of that branch, and says what it runs
differently from the app: every component whose image differs, with the findings of both.
Everything comes from the cluster and the registry, never from Git; the diff of the code is the
pull request's own.

The Markdown is meant to be posted as it is, for example with
` + "`gh pr comment --body-file -`" + `. With --json, the same comes as a Report document.`,
		Example: `  shelf app report hello --preview "$(git branch --show-current)" | gh pr comment --body-file -`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ops.CheckAppName(name); err != nil {
				return err
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			r, err := shelf.Report(cmd.Context(), name, preview)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), reportDoc(name, r))
			}
			printReport(cmd.OutOrStdout(), name, r)
			return nil
		},
	}
	target.registerTarget(cmd.Flags())
	registerJSON(cmd, &asJSON)
	cmd.Flags().StringVar(&preview, "preview", "", "the preview of this branch (its name or its slug) instead of the app")
	return cmd
}

// openCommand is how the reader of a report reaches what it is about.
func openCommand(app string, r ops.Report) string {
	if r.Branch != "" {
		return "shelf app open " + app + " --preview " + r.Branch
	}
	return "shelf app open " + app
}

// printReport writes the report as Markdown, in the order a reviewer asks: does it run, did its
// tests pass, can it be trusted, what does it change.
func printReport(out io.Writer, app string, r ops.Report) {
	a := r.App
	if r.Branch != "" {
		fmt.Fprintf(out, "### shelf: preview of %s (%s)\n\n", r.Branch, a.Name)
	} else {
		fmt.Fprintf(out, "### shelf: %s\n\n", a.Name)
	}
	state := fmt.Sprintf("**%s**", r.Diagnosis.Phase())
	if a.Commit != "" {
		state += fmt.Sprintf(" at commit `%s`", a.Commit[:min(len(a.Commit), 12)])
	}
	fmt.Fprintln(out, state+".")
	if t := r.Diagnosis.Trouble(); t != nil {
		fmt.Fprintf(out, "Not ready at %s: %s\n", t.Name, firstLine(t.Message))
		if t.Hint != "" {
			fmt.Fprintf(out, "%s.\n", strings.TrimSuffix(t.Hint, "."))
		}
	}
	fmt.Fprintf(out, "Open it with `%s`.\n\n", openCommand(app, r))

	printReportTests(out, r.Diagnosis.Tests)
	printReportVerification(out, a.Verification)
	if c := r.Comparison; c != nil {
		printComparison(out, c)
	}
	if iso := a.Isolation; iso != nil {
		level := iso.PodSecurity
		if level == "" {
			level = "none (the platform predates it)"
		}
		lan := "no private addresses"
		if len(iso.LAN) > 0 {
			lan = "private addresses " + strings.Join(iso.LAN, ", ")
		}
		fmt.Fprintf(out, "**Isolation:** Pod Security %s; reaches its own components, DNS, the internet and %s.\n", level, lan)
	}
}

func printReportTests(out io.Writer, tests []cluster.TestResult) {
	if len(tests) == 0 {
		fmt.Fprintln(out, "**Tests:** none declared in app.yaml.")
		fmt.Fprintln(out)
		return
	}
	fmt.Fprintln(out, "**Tests:**")
	for _, t := range tests {
		switch t.Phase {
		case cluster.PhaseReady:
			fmt.Fprintf(out, "- %s passed\n", t.Name)
		case cluster.PhaseFailed:
			line := "- " + t.Name + " failed"
			if t.ExitCode != nil {
				line += fmt.Sprintf(" with exit code %d", *t.ExitCode)
			}
			fmt.Fprintln(out, line)
			if t.Log != "" {
				fmt.Fprintf(out, "  <details><summary>end of its log</summary>\n\n  ```\n")
				for _, l := range strings.Split(t.Log, "\n") {
					fmt.Fprintf(out, "  %s\n", l)
				}
				fmt.Fprintf(out, "  ```\n  </details>\n")
			}
		default:
			fmt.Fprintf(out, "- %s is still running\n", t.Name)
		}
	}
	fmt.Fprintln(out)
}

func printReportVerification(out io.Writer, v *ops.Verification) {
	if v == nil {
		return
	}
	if v.Verified {
		fmt.Fprintf(out, "**Verification:** verified, signed by %s.\n", v.Signer)
	} else {
		fmt.Fprintf(out, "**Verification:** not verified: %s.\n", v.Reason)
	}
	if v.Scan != nil {
		fmt.Fprintf(out, "\n**Scan** (%s, %s):\n\n| Component | Critical | High |\n|---|---|---|\n", v.Scan.Scanner, v.Scan.ScannedAt)
		for _, img := range v.Scan.Images {
			if img.Scanned {
				fmt.Fprintf(out, "| %s | %d | %d |\n", img.Component, img.Critical, img.High)
			} else {
				fmt.Fprintf(out, "| %s | not scanned | |\n", img.Component)
			}
		}
	}
	fmt.Fprintln(out)
}

func printComparison(out io.Writer, c *ops.Comparison) {
	against := c.App
	if c.Commit != "" {
		against += fmt.Sprintf(" at commit `%s`", c.Commit[:min(len(c.Commit), 12)])
	}
	if !c.Comparable {
		fmt.Fprintf(out, "**Compared with %s:** cannot say, %s.\n\n", against, c.Reason)
		return
	}
	if len(c.Changed) == 0 {
		fmt.Fprintf(out, "**Compared with %s:** the same images.\n\n", against)
		return
	}
	fmt.Fprintf(out, "**Compared with %s:**\n\n| Component | Image | Critical | High |\n|---|---|---|---|\n", against)
	for _, ch := range c.Changed {
		switch {
		case ch.AppDigest == "":
			fmt.Fprintf(out, "| %s | new | %d | %d |\n", ch.Component, ch.Critical, ch.High)
		case ch.Digest == "":
			fmt.Fprintf(out, "| %s | removed | | |\n", ch.Component)
		default:
			fmt.Fprintf(out, "| %s | changed | %d (was %d) | %d (was %d) |\n",
				ch.Component, ch.Critical, ch.AppCritical, ch.High, ch.AppHigh)
		}
	}
	fmt.Fprintf(out, "\nAll images: %d critical, %d high (%s: %d critical, %d high).\n\n",
		c.Critical, c.High, c.App, c.AppCritical, c.AppHigh)
}

type reportDocument struct {
	header
	Name string `json:"name"`
	// PreviewOf and Branch are set for the report of a preview.
	PreviewOf string        `json:"previewOf,omitempty"`
	Branch    string        `json:"branch,omitempty"`
	Phase     cluster.Phase `json:"phase"`
	Host      string        `json:"host,omitempty"`
	// Open is the command that reaches what the report is about.
	Open   string `json:"open"`
	Commit string `json:"commit,omitempty"`
	// Stage is the first stage that is not Ready, left out when everything is.
	Stage        *stageDocument        `json:"stage,omitempty"`
	Tests        []testDocument        `json:"tests,omitempty"`
	Verification *verificationDocument `json:"verification,omitempty"`
	Isolation    *isolationDocument    `json:"isolation,omitempty"`
	// Comparison is what a preview runs differently from its app.
	Comparison *comparisonDocument `json:"comparison,omitempty"`
}

type comparisonDocument struct {
	App        string `json:"app"`
	Commit     string `json:"commit,omitempty"`
	Comparable bool   `json:"comparable"`
	Reason     string `json:"reason,omitempty"`
	// Changed are the components whose image differs; a digest is left out on the side that
	// does not have the component.
	Changed     []imageChangeDocument `json:"changed"`
	Critical    int                   `json:"critical"`
	High        int                   `json:"high"`
	AppCritical int                   `json:"appCritical"`
	AppHigh     int                   `json:"appHigh"`
}

type imageChangeDocument struct {
	Component   string `json:"component"`
	Digest      string `json:"digest,omitempty"`
	AppDigest   string `json:"appDigest,omitempty"`
	Critical    int    `json:"critical"`
	High        int    `json:"high"`
	AppCritical int    `json:"appCritical"`
	AppHigh     int    `json:"appHigh"`
}

func reportDoc(app string, r ops.Report) reportDocument {
	status := appStatus(r.App, r.Diagnosis)
	doc := reportDocument{
		header: newHeader("Report"),
		Name:   r.App.Name, Branch: r.Branch, Phase: r.Diagnosis.Phase(), Host: r.App.Host,
		Open: openCommand(app, r), Commit: r.App.Commit,
		Tests: status.Tests, Verification: status.Verification, Isolation: status.Isolation,
	}
	if r.Branch != "" {
		doc.PreviewOf = app
	}
	if t := r.Diagnosis.Trouble(); t != nil {
		s := stageDocument{Name: t.Name, Object: t.Object, Phase: t.Phase, Reason: t.Reason, Message: t.Message, Hint: t.Hint}
		doc.Stage = &s
	}
	if c := r.Comparison; c != nil {
		doc.Comparison = &comparisonDocument{App: c.App, Commit: c.Commit, Comparable: c.Comparable, Reason: c.Reason,
			Changed: []imageChangeDocument{}, Critical: c.Critical, High: c.High, AppCritical: c.AppCritical, AppHigh: c.AppHigh}
		for _, ch := range c.Changed {
			doc.Comparison.Changed = append(doc.Comparison.Changed, imageChangeDocument(ch))
		}
	}
	return doc
}
