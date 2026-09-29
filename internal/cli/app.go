package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
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

// Environment variables with the credentials of an app. A token is never taken from a flag, so
// it does not show up in process listings or shell history; and it is only read when a flag
// asks for it, so a variable left in the shell does not end up in every app added from it.
const (
	envRegistryUser      = "GHCR_USERNAME"
	envRegistryToken     = "GHCR_TOKEN"
	envCloudflareToken   = "CF_API_TOKEN"
	envCloudflareAccount = "CF_ACCOUNT_ID"
)

// accessHelp explains where the credentials of an app come from, for every command that takes
// them.
const accessHelp = `--registry-login stores ` + envRegistryUser + ` and ` + envRegistryToken + ` (a classic PAT with
read:packages) as the app's login for ghcr.io: its deploy artifact and its images are pulled with
it, and nothing else uses it. Without one, the app reads the registry anonymously.

--cloudflare exposes the app through a Cloudflare tunnel of its own, in the account of
` + envCloudflareToken + ` (or ` + envCloudflareAccount + `, when the token sees several). The token needs Account:Cloudflare
Tunnel:Edit, and Zone:DNS:Edit for the app's domain, which has to be a Cloudflare zone of that
account. The token is kept in $SHELF_HOME/apps/<name>/cloudflare.yaml on this machine and never
goes into the cluster, so only this machine can move or remove the app's record and tunnel.`

// accessFlags are the credentials an app can be given.
type accessFlags struct {
	domain       string
	registry     bool
	noRegistry   bool
	cloudflare   bool
	noCloudflare bool
}

// register adds the flags; removable adds the ones that take a credential away again.
func (f *accessFlags) register(fs *pflag.FlagSet, removable bool) {
	fs.StringVar(&f.domain, "domain", "",
		"a domain of the app's own: it answers at <name><host-suffix>.<domain> (default: the cluster's)")
	fs.BoolVar(&f.registry, "registry-login", false,
		"store "+envRegistryUser+" and "+envRegistryToken+" as the app's login for "+cluster.RegistryHost)
	fs.BoolVar(&f.cloudflare, "cloudflare", false,
		"expose the app through its own tunnel, with "+envCloudflareToken+" and "+envCloudflareAccount)
	if removable {
		fs.BoolVar(&f.noRegistry, "no-registry-login", false, "drop the app's registry login")
		fs.BoolVar(&f.noCloudflare, "no-cloudflare", false,
			"take the app off the internet: delete its record and its tunnel")
	}
}

// changes reports whether any flag asks for a change.
func (f *accessFlags) changes() bool {
	return f.domain != "" || f.registry || f.noRegistry || f.cloudflare || f.noCloudflare
}

// access reads the credentials the flags ask for from the environment.
func (f *accessFlags) access(o Options) (ops.Access, error) {
	a := ops.Access{
		Domain:           f.domain,
		RemoveRegistry:   f.noRegistry,
		RemoveCloudflare: f.noCloudflare,
	}
	if f.registry {
		user, token := o.getenv(envRegistryUser), o.getenv(envRegistryToken)
		if user == "" || token == "" {
			return ops.Access{}, fmt.Errorf("--registry-login needs %s and %s", envRegistryUser, envRegistryToken)
		}
		a.Registry = &cluster.RegistryAuth{Username: user, Token: token}
	}
	if f.cloudflare {
		token := strings.TrimSpace(o.getenv(envCloudflareToken))
		if token == "" {
			return ops.Access{}, fmt.Errorf("--cloudflare needs %s", envCloudflareToken)
		}
		a.Cloudflare = &hostcfg.Cloudflare{Token: token, Account: strings.TrimSpace(o.getenv(envCloudflareAccount))}
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
values, and the app's credentials unless they are given again.

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
			given, err := access.access(o)
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
		Short: "Change an app's registry login, its domain or its Cloudflare access",
		Long: `Change how an app reaches its registry and the internet, and nothing else: the app is
deployed again from the artifact it is registered with. What is not given stays as it is.

Moving the app to another domain or another Cloudflare account deletes its record and its tunnel
there, and --no-cloudflare takes it off the internet.

` + accessHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ops.CheckAppName(name); err != nil {
				return err
			}
			if !access.changes() {
				return fmt.Errorf("nothing to change; pass --domain, --registry-login, --cloudflare, " +
					"--no-registry-login or --no-cloudflare")
			}
			given, err := access.access(o)
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
