package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
)

func newAppCmd(o Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Add, inspect and remove apps",
	}
	cmd.AddCommand(
		newAppAddCmd(o), newAppRmCmd(o), newAppStatusCmd(o),
		newAppRedeployCmd(o), newAppSecretsCmd(o), newAppCredentialsCmd(o),
	)
	return cmd
}

// accessHelp explains what an app can be given, for every command that takes it.
const accessHelp = `--registry names the registry connection the app pulls its deploy artifact and its images
with; without one, it reads the registry anonymously. --cloudflare names the Cloudflare
connection it is exposed through: it gets a tunnel of its own in that connection's account, and
its domain has to be a zone of that account. Connections are defined with ` + "`shelf connection add`" + `.`

// accessFlags choose what an app reaches its registry and the internet with.
type accessFlags struct {
	domain       string
	registry     string
	noRegistry   bool
	cloudflare   string
	noCloudflare bool
}

// register adds the flags; removable adds the ones that take a connection away again.
func (f *accessFlags) register(fs *pflag.FlagSet, removable bool) {
	fs.StringVar(&f.domain, "domain", "",
		"a domain of the app's own: it answers at <name><host-suffix>.<domain> (default: the cluster's)")
	fs.StringVar(&f.registry, "registry", "", "the registry connection the app pulls with")
	fs.StringVar(&f.cloudflare, "cloudflare", "", "the Cloudflare connection the app is exposed through")
	if removable {
		fs.BoolVar(&f.noRegistry, "no-registry", false, "pull without a login")
		fs.BoolVar(&f.noCloudflare, "no-cloudflare", false,
			"take the app off the internet: delete its record and its tunnel")
	}
}

// changes reports whether any flag asks for a change.
func (f *accessFlags) changes() bool {
	return f.domain != "" || f.registry != "" || f.noRegistry || f.cloudflare != "" || f.noCloudflare
}

func (f *accessFlags) access() (ops.Access, error) {
	a := ops.Access{
		Domain:           f.domain,
		Registry:         f.registry,
		RemoveRegistry:   f.noRegistry,
		Cloudflare:       f.cloudflare,
		RemoveCloudflare: f.noCloudflare,
	}
	return a, a.Check()
}

func newAppAddCmd(o Options) *cobra.Command {
	var (
		insecure bool
		access   accessFlags
		target   clusterFlags
	)
	cmd := &cobra.Command{
		Use:   "add <name> <oci://registry/repository:tag>",
		Short: "Deploy an app from its deploy artifact, and keep it updated",
		Long: `Add an app to the platform. From then on, Flux deploys every new version of the deploy
artifact under the given tag, usually "main".

shelf reads the artifact, generates the secrets the app declares and stores them in the cluster
and in a backup file on this machine (` + "$SHELF_HOME/apps/<name>/secrets.yaml, default ~/.shelf" + `).
If the cluster was rebuilt, the backup restores the old values. Running add again updates the
artifact reference, generates secrets that were added to app.yaml since, and keeps all existing
values, and the app's domain and connections unless others are given.

` + accessHelp,
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
			given, err := access.access()
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
				Access:   given,
				Timeout:  target.timeout,
			}, progress.Writer(out))
		},
	}
	cmd.Flags().BoolVar(&insecure, "insecure-registry", false, "pull the deploy artifact without TLS (dev registry)")
	access.register(cmd.Flags(), false)
	target.register(cmd.Flags())
	return cmd
}

func newAppCredentialsCmd(o Options) *cobra.Command {
	var (
		access accessFlags
		target clusterFlags
	)
	cmd := &cobra.Command{
		Use:   "credentials <name>",
		Short: "Change an app's domain or the connections it uses",
		Long: `Change how an app reaches its registry and the internet, and nothing else: the app is
deployed again from the artifact it is registered with. What is not given stays as it is.

Moving the app to another domain or another Cloudflare connection deletes its record, and its
tunnel if the account changes; --no-cloudflare takes it off the internet.

` + accessHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ops.CheckAppName(name); err != nil {
				return err
			}
			if !access.changes() {
				return fmt.Errorf("nothing to change; pass --domain, --registry, --cloudflare, " +
					"--no-registry or --no-cloudflare")
			}
			given, err := access.access()
			if err != nil {
				return err
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Changing the credentials of app %s on:\n", name)
			printTarget(out, shelf.Target)
			return shelf.SetAccess(cmd.Context(), name, given, target.timeout, progress.Writer(out))
		},
	}
	access.register(cmd.Flags(), true)
	target.register(cmd.Flags())
	return cmd
}

func newAppRmCmd(o Options) *cobra.Command {
	var target clusterFlags
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an app with all its data",
		Long: `Remove an app: its namespace with all objects and volumes, its secrets in the cluster,
and, if it is exposed, its DNS record and its Cloudflare tunnel. The secret backup on this
machine is kept.`,
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
