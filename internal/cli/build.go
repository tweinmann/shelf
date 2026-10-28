package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/schema"
)

var componentNameRE = regexp.MustCompile(schema.ComponentNamePattern)

// buildPlan is what a CI needs before it can render an app.yaml: the package name, app.yaml's
// `name`, and the components and tests it has to build. The app's name in shelf is not part of it: that
// is chosen at `shelf app add`.
type buildPlan struct {
	Package string      `json:"package"`
	Builds  []buildItem `json:"builds"`
	// Tag is the tag a branch's deploy artifact is published under, with --branch only.
	Tag string `json:"tag,omitempty"`
}

// buildItem is one component or test that has to be built before rendering. A test is listed
// under component as well: the workflow builds and pushes both alike, as <package>/<name>.
type buildItem struct {
	Component string `json:"component"`
	// Context is the build directory, relative to the working directory.
	Context string `json:"context"`
}

func newBuildPlanCmd() *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "build-plan <app.yaml>",
		Short: "Print the package name and the components and tests that have to be built, as JSON",
		Long: `Print {package, builds} as JSON: the package name (app.yaml's name), and every component
and then every test with a build directory. The build contexts are relative to the app.yaml, and
printed relative to the working directory.

A CI workflow publishes the deploy artifact as ghcr.io/<owner>/<package> and the images below
it: it builds each one, pushes the image and passes the result to
` + "`shelf render --image <name>=<reference>`" + `. The app's name in shelf is chosen at
` + "`shelf app add`" + `, so one package can be deployed as several apps.

With --branch, the plan also holds the tag a push to that branch publishes the deploy artifact
under, branch-<slug>. An app with previews runs it as <app>--<slug>.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := args[0]
			doc, err := readDocument(file)
			if err != nil {
				return err
			}
			plan := buildPlan{Package: doc.App.Name, Builds: []buildItem{}}
			if branch != "" {
				if plan.Tag = schema.BranchTag(branch); plan.Tag == schema.BranchTagPrefix {
					return fmt.Errorf("branch %q has no letters or digits to name a preview by", branch)
				}
			}
			add := func(what, name, build string) error {
				context := filepath.Join(filepath.Dir(file), build)
				if info, err := os.Stat(context); err != nil || !info.IsDir() {
					return fmt.Errorf("%s %s: build directory %s does not exist", what, name, context)
				}
				plan.Builds = append(plan.Builds, buildItem{Component: name, Context: context})
				return nil
			}
			for _, compName := range slices.Sorted(maps.Keys(doc.App.Components)) {
				if comp := doc.App.Components[compName]; comp != nil && comp.IsBuilt() {
					if err := add("component", compName, comp.Build); err != nil {
						return err
					}
				}
			}
			for _, testName := range slices.Sorted(maps.Keys(doc.App.Tests)) {
				if test := doc.App.Tests[testName]; test != nil && test.IsBuilt() {
					if err := add("test", testName, test.Build); err != nil {
						return err
					}
				}
			}
			out, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return err
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "also print the tag a push to this branch publishes, branch-<slug>")
	return cmd
}

// parseImages turns the --image flags (name=reference, for a component or a test) into a map.
func parseImages(flags []string) (map[string]string, error) {
	images := map[string]string{}
	for _, f := range flags {
		n, ref, ok := strings.Cut(f, "=")
		switch {
		case !ok || n == "" || ref == "":
			return nil, fmt.Errorf("--image %q must be <name>=<reference>", f)
		case !componentNameRE.MatchString(n):
			return nil, fmt.Errorf("--image %q: %q is not a component or test name", f, n)
		}
		if _, dup := images[n]; dup {
			return nil, fmt.Errorf("--image: %s is given twice", n)
		}
		images[n] = ref
	}
	return images, nil
}
