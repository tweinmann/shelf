// Command shelf is the CLI of the shelf app platform.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/tweinmann/shelf/internal/cli"
	"github.com/tweinmann/shelf/internal/render"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Execute(ctx, cli.New(render.NewRegistryResolver()))
	stop()
	os.Exit(code)
}
