package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/deploy"
	"github.com/tweinmann/shelf/internal/schema"
	"github.com/tweinmann/shelf/internal/secrets"
)

// Cluster and registry access of the app commands; replaced in tests.
var (
	fetchApp   = deploy.Fetch
	appSecrets = cluster.AppSecrets
	addApp     = cluster.AddApp
	removeApp  = cluster.RemoveApp
)

var appNameRE = regexp.MustCompile(schema.AppNamePattern)

// shelfHome is where shelf keeps state on the host: $SHELF_HOME, or ~/.shelf.
func shelfHome() (string, error) {
	if dir := os.Getenv("SHELF_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".shelf"), nil
}

func secretBackup() (secrets.Backup, error) {
	home, err := shelfHome()
	if err != nil {
		return secrets.Backup{}, err
	}
	return secrets.Backup{Dir: filepath.Join(home, "apps")}, nil
}

func checkAppName(name string) error {
	if len(name) > schema.MaxAppNameLength || !appNameRE.MatchString(name) {
		return fmt.Errorf("app name %q must be a DNS label of at most %d characters", name, schema.MaxAppNameLength)
	}
	return nil
}

func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Add and remove apps",
	}
	cmd.AddCommand(newAppAddCmd(), newAppRmCmd())
	return cmd
}

func newAppAddCmd() *cobra.Command {
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
			if err := checkAppName(name); err != nil {
				return err
			}
			artifact, err := cluster.ParseArtifact(args[1])
			if err != nil {
				return err
			}
			backup, err := secretBackup()
			if err != nil {
				return err
			}
			t, err := target.load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Adding app %s from %s to:\n", name, artifact)
			t.print(out)

			app, err := fetchApp(cmd.Context(), artifact.Reference(), insecure)
			if err != nil {
				return err
			}
			if app.Name != name {
				return fmt.Errorf("the artifact deploys app %q, not %q", app.Name, name)
			}

			stored, err := appSecrets(cmd.Context(), t.config, name)
			if err != nil {
				return err
			}
			saved, err := backup.Load(name)
			if err != nil {
				return err
			}
			declared := deploy.SecretNames(app)
			values, sources := secrets.Merge(declared, stored, saved)
			for _, s := range declared {
				fmt.Fprintf(out, "secret %s: %s\n", s, sources[s])
			}
			// The backup is written first, so a generated value never exists only in the cluster.
			if len(values) > 0 {
				if err := backup.Save(name, values); err != nil {
					return fmt.Errorf("writing the secret backup: %w", err)
				}
				fmt.Fprintf(out, "secret backup: %s\n", backup.Path(name))
			}

			publisher, err := newDNS(cmd.Context(), t.config, out)
			if err != nil {
				return err
			}
			if err := addApp(cmd.Context(), t.config, cluster.AppOptions{
				Name:     name,
				Artifact: artifact,
				Insecure: insecure,
				Secrets:  values,
				Timeout:  target.timeout,
				Out:      out,
			}); err != nil {
				return err
			}
			return publisher.publish(cmd.Context(), name, out)
		},
	}
	cmd.Flags().BoolVar(&insecure, "insecure-registry", false, "pull the deploy artifact without TLS (dev registry)")
	target.register(cmd.Flags())
	return cmd
}

func newAppRmCmd() *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an app with all its data",
		Long: `Remove an app: its namespace with all objects and volumes, and its secrets in the
cluster. The secret backup on this machine is kept.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := checkAppName(name); err != nil {
				return err
			}
			t, err := target.load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Removing app %s, including all its volumes, from:\n", name)
			t.print(out)
			if !target.yes {
				ok, err := confirm(cmd.InOrStdin(), out)
				if err != nil {
					return err
				}
				if !ok {
					return errAborted
				}
			}
			publisher, err := newDNS(cmd.Context(), t.config, out)
			if err != nil {
				return err
			}
			found, err := removeApp(cmd.Context(), t.config, name, target.timeout, out)
			if err != nil {
				return err
			}
			if err := publisher.withdraw(cmd.Context(), name, out); err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("app %s does not exist", name)
			}
			if backup, err := secretBackup(); err == nil {
				if _, err := os.Stat(backup.Path(name)); err == nil {
					fmt.Fprintf(out, "The secret backup stays in %s; delete it if you do not need the values any more.\n",
						backup.Path(name))
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			return nil
		},
	}
	target.register(cmd.Flags())
	return cmd
}
