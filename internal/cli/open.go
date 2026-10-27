package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/ops"
)

// openAddress is the only address `shelf app open` listens on. There is no flag for another one:
// a listener on the LAN would be an exposure, and exposure is the operator's decision, made
// with `shelf app add` and audit-logged, not a side effect of looking at an app.
const openAddress = "127.0.0.1"

func newAppOpenCmd(o Options) *cobra.Command {
	var (
		target  clusterFlags
		port    int
		browser bool
	)
	cmd := &cobra.Command{
		Use:   "open <name>",
		Short: "Reach an app from a browser on this machine, through the cluster's Traefik",
		Long: `Listen on a port of 127.0.0.1 and carry every request through the Kubernetes API server to
the cluster's Traefik, with the Host header of the app's own host. Traefik routes it exactly as it
routes a tunnel's requests, so the app's routes and stripPrefix behave as they do on the
internet. This works for apps that stay inside the cluster, too.

This is not an exposure: nothing about the app changes, nothing listens anywhere but on this
machine's loopback interface, and the address works only for as long as this command runs. Stop
it with Ctrl-C.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ops.CheckAppName(name); err != nil {
				return err
			}
			if port < 0 || port > 65535 {
				return fmt.Errorf("--port %d is not a port", port)
			}
			shelf, err := target.load(o)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			proxy, err := shelf.OpenApp(ctx, name)
			if err != nil {
				return err
			}
			defer proxy.Close()
			ln, err := net.Listen("tcp", net.JoinHostPort(openAddress, strconv.Itoa(port)))
			if err != nil {
				return fmt.Errorf("cannot listen on %s:%d: %w", openAddress, port, err)
			}
			local := ln.Addr().String()
			url := "http://" + local + "/"
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s (%s) is at %s\n", name, proxy.Host, url)
			fmt.Fprintln(cmd.ErrOrStderr(), "Only this machine can reach it, and only while this runs. Press Ctrl-C to stop.")

			srv := &http.Server{Handler: proxy.Handler(local), ReadHeaderTimeout: 30 * time.Second}
			served := make(chan error, 1)
			go func() { served <- srv.Serve(ln) }()
			if browser {
				if err := o.openBrowser(url); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "cannot open a browser: %v\n", err)
				}
			}
			select {
			case err := <-served:
				return err
			case <-ctx.Done():
			}
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	target.registerTarget(cmd.Flags())
	cmd.Flags().IntVar(&port, "port", 0, "the local port; a free one when not given")
	cmd.Flags().BoolVar(&browser, "browser", false, "open the address in the default browser")
	return cmd
}
