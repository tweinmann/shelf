package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/server"
)

// DefaultListen is where the admin UI listens: every address of the machine, so it can be
// reached from the rest of the home network, on a port nothing else uses.
const DefaultListen = ":7654"

func newServeCmd(o Options) *cobra.Command {
	var (
		listen string
		target clusterFlags
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the admin UI",
		Long: `Serve the admin UI, which shows the apps of this cluster and how they are doing.

It listens on the local network without TLS, so the password crosses the network in the clear.
That is the trade for a machine at home that has no name a certificate could be issued for; to
keep it off the network, pass --listen 127.0.0.1:7654 and reach it through an SSH forward.

The first start prints a setup code. It claims the instance: until it has been used, the server
shows nothing but the page that asks for it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			home, err := o.shelfHome()
			if err != nil {
				return err
			}
			store, err := hostcfg.Open(home)
			if err != nil {
				return err
			}
			srv, err := server.New(server.Options{
				Platform: shelf,
				Store:    store,
				Version:  o.version(),
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			listener, err := net.Listen("tcp", listen)
			if err != nil {
				return err
			}
			local, remote := uiURLs(listener.Addr(), hostName())
			fmt.Fprintf(out, "The admin UI is at %s\n", local)
			if remote != "" {
				fmt.Fprintf(out, "  from another device on the network: %s\n", remote)
			}
			fmt.Fprintf(out, "  cluster   %s\n", shelf.Target.Context)
			fmt.Fprintf(out, "  state     %s\n", store.Dir())
			if token := srv.SetupToken(); token != "" {
				fmt.Fprintf(out, "\nThis shelf has no owner yet. Open the page and enter this setup code:\n\n  %s\n\n", token)
			}
			return serve(cmd.Context(), listener, srv.Handler(), out)
		},
	}
	f := cmd.Flags()
	f.StringVar(&listen, "listen", DefaultListen, "address to listen on; 127.0.0.1:7654 keeps it off the network")
	target.registerTarget(f)
	return cmd
}

// serve runs until the context ends, then lets the open requests finish.
func serve(ctx context.Context, listener net.Listener, handler http.Handler, out interface{ Write([]byte) (int, error) }) error {
	srv := &http.Server{
		Handler: handler,
		// A request that stalls while sending its headers must not hold a connection open.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	done := make(chan error, 1)
	go func() {
		<-ctx.Done()
		fmt.Fprintln(out, "\nStopping.")
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		done <- srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-done
}

// uiURLs turns a listening address into addresses that can be pasted into a browser. A server
// that listens on every address has two of them, and only the person in front of it knows which
// one applies: localhost works on the machine itself, and the machine's own name works from
// another device — if that name resolves there, which it does over mDNS on a Mac but not for a
// container whose name is a hexadecimal id.
func uiURLs(addr net.Addr, hostname string) (local, remote string) {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://" + addr.String() + "/", ""
	}
	if host != "" && host != "::" && host != "0.0.0.0" {
		return "http://" + net.JoinHostPort(host, port) + "/", ""
	}
	local = "http://" + net.JoinHostPort("localhost", port) + "/"
	if hostname != "" && hostname != "localhost" {
		remote = "http://" + net.JoinHostPort(hostname, port) + "/"
	}
	return local, remote
}

func hostName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
