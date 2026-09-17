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

// buildItem is one component that has to be built before rendering.
type buildItem struct {
	Component string `json:"component"`
	// Context is the build directory, relative to the working directory.
	Context string `json:"context"`
}

func newBuildPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build-plan <app.yaml>",
		Short: "List the components that have to be built, as JSON",
		Long: `Print the components with a build directory, as a JSON array of {component, context}.
The build contexts are relative to the app.yaml, and printed relative to the working directory.

A CI workflow builds each of them, pushes the image and passes the result to
` + "`shelf render --image <component>=<reference>`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := args[0]
			doc, err := readDocument(file)
			if err != nil {
				return err
			}
			plan := []buildItem{}
			for _, compName := range slices.Sorted(maps.Keys(doc.App.Components)) {
				comp := doc.App.Components[compName]
				if comp == nil || !comp.IsBuilt() {
					continue
				}
				context := filepath.Join(filepath.Dir(file), comp.Build)
				if info, err := os.Stat(context); err != nil || !info.IsDir() {
					return fmt.Errorf("component %s: build directory %s does not exist", compName, context)
				}
				plan = append(plan, buildItem{Component: compName, Context: context})
			}
			out, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return err
		},
	}
}

// parseImages turns the --image flags (component=reference) into a map.
func parseImages(flags []string) (map[string]string, error) {
	images := map[string]string{}
	for _, f := range flags {
		component, ref, ok := strings.Cut(f, "=")
		switch {
		case !ok || component == "" || ref == "":
			return nil, fmt.Errorf("--image %q must be <component>=<reference>", f)
		case !componentNameRE.MatchString(component):
			return nil, fmt.Errorf("--image %q: %q is not a component name", f, component)
		}
		if _, dup := images[component]; dup {
			return nil, fmt.Errorf("--image: component %s is given twice", component)
		}
		images[component] = ref
	}
	return images, nil
}
