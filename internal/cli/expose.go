package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/cloudflare"
	"github.com/tweinmann/shelf/internal/cluster"
)

// Environment variables with the Cloudflare credentials. The token is never a flag, so it does
// not show up in process listings or shell history.
const (
	envCloudflareToken   = "CF_API_TOKEN"
	envCloudflareAccount = "CF_ACCOUNT_ID"
)

// Cluster access of the expose command; replaced in tests.
var (
	clusterSettings   = cluster.ClusterSettings
	tunnelCredentials = cluster.TunnelCredentials
	exposeCluster     = cluster.Expose
	newCloudflare     = func(token string) cloudflareAPI { return cloudflare.New(token) }
)

// cloudflareAPI is the part of the Cloudflare API shelf uses.
type cloudflareAPI interface {
	AccountID(ctx context.Context) (string, error)
	FindTunnel(ctx context.Context, account, name string) (*cloudflare.Tunnel, error)
	CreateTunnel(ctx context.Context, account, name string) (*cloudflare.Tunnel, []byte, error)
	DeleteTunnel(ctx context.Context, account, id string) error
}

func newInitExposeCmd() *cobra.Command {
	var (
		tunnelName string
		owner      string
		target     clusterFlags
	)
	cmd := &cobra.Command{
		Use:   "expose",
		Short: "Make the apps of this cluster reachable from the internet",
		Long: `Connect the cluster to Cloudflare: find or create a tunnel, run cloudflared with a single
rule that forwards everything to Traefik, and let external-dns publish one DNS record per app.

The Cloudflare API token comes from ` + envCloudflareToken + `; it needs Account:Cloudflare
Tunnel:Edit and Zone:DNS:Edit for the zone. With several accounts, set ` + envCloudflareAccount + `.

Requires ` + "`shelf init cluster`" + `, whose domain and host suffix decide the names the apps get.
It is idempotent: an existing tunnel of the same name is reused as long as the cluster still
holds its credentials.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := os.Getenv(envCloudflareToken)
			if token == "" {
				return fmt.Errorf("%s is not set; it needs Account:Cloudflare Tunnel:Edit and Zone:DNS:Edit",
					envCloudflareToken)
			}
			t, err := target.load()
			if err != nil {
				return err
			}
			settings, err := clusterSettings(cmd.Context(), t.config)
			if err != nil {
				return err
			}
			if settings.Domain == "" {
				return fmt.Errorf("this cluster has no domain; run `shelf init cluster --domain <domain>` first")
			}
			if tunnelName == "" {
				tunnelName = "shelf" + settings.HostSuffix
			}
			if owner == "" {
				owner = tunnelName
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "Exposing the apps of:")
			t.print(out)
			fmt.Fprintf(out, "  hosts     %s\n", "<app>"+settings.HostSuffix+"."+settings.Domain)
			fmt.Fprintf(out, "  tunnel    %s\n", tunnelName)
			fmt.Fprintf(out, "  dns owner %s\n", owner)

			api := newCloudflare(token)
			account := os.Getenv(envCloudflareAccount)
			if account == "" {
				if account, err = api.AccountID(cmd.Context()); err != nil {
					return err
				}
			}
			tunnel, credentials, err := findOrCreateTunnel(cmd, api, account, tunnelName, t.config, target.yes)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "  target    %s\n", tunnel.Target())
			if !target.yes {
				ok, err := confirm(cmd.InOrStdin(), out)
				if err != nil {
					return err
				}
				if !ok {
					return errAborted
				}
			}
			return exposeCluster(cmd.Context(), t.config, cluster.ExposeOptions{
				TunnelID:    tunnel.ID,
				Credentials: credentials,
				APIToken:    token,
				Owner:       owner,
				Domain:      settings.Domain,
				Timeout:     target.timeout,
				Out:         out,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&tunnelName, "tunnel", "", "name of the Cloudflare tunnel (default: shelf<host suffix>)")
	f.StringVar(&owner, "dns-owner", "",
		"identifier external-dns writes into its TXT records, so clusters can share a zone (default: the tunnel name)")
	target.register(f)
	return cmd
}

// findOrCreateTunnel returns the tunnel to use and, if it was created, its credentials. A tunnel
// that exists without credentials in the cluster is useless, because the secret exists only
// once; with the user's consent it is replaced.
func findOrCreateTunnel(cmd *cobra.Command, api cloudflareAPI, account, name string, cfg *rest.Config, yes bool) (
	*cloudflare.Tunnel, []byte, error) {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	existing, err := api.FindTunnel(ctx, account, name)
	if err != nil {
		return nil, nil, err
	}
	if existing == nil {
		tunnel, credentials, err := api.CreateTunnel(ctx, account, name)
		if err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(out, "  tunnel %s created\n", name)
		return tunnel, credentials, nil
	}

	stored, err := tunnelCredentials(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if len(stored) > 0 && strings.Contains(string(stored), existing.ID) {
		return existing, nil, nil
	}
	fmt.Fprintf(out, "  tunnel %s exists, but this cluster does not hold its credentials.\n", name)
	fmt.Fprintln(out, "  Its secret cannot be read again, so it has to be replaced.")
	if !yes {
		ok, err := confirm(cmd.InOrStdin(), out)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, errAborted
		}
	}
	if err := api.DeleteTunnel(ctx, account, existing.ID); err != nil {
		return nil, nil, fmt.Errorf("replacing the tunnel: %w; a tunnel with open connections cannot be deleted", err)
	}
	tunnel, credentials, err := api.CreateTunnel(ctx, account, name)
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(out, "  tunnel %s replaced\n", name)
	return tunnel, credentials, nil
}
