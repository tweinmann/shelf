// Command shelf is the CLI of the shelf app platform.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/tweinmann/shelf/internal/cli"
	"github.com/tweinmann/shelf/internal/render"
)

func main() {
	// SIGTERM as well as Ctrl-C: `shelf serve` runs as a service, and that is how launchd and
	// every other supervisor asks a process to stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, cli.New(cli.Options{Images: render.NewRegistryResolver()}))
	stop()
	os.Exit(code)
}
