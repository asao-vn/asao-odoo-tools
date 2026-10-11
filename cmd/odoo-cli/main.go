// Command odoo-cli is the standalone Odoo client: the operation catalog, run directly against an
// Odoo the local operator configured, with no ASAO account and no ASAO services.
//
// It talks directly to the Odoo configured by the local operator. It deliberately shares the
// standalone command tree with the historical `aerp` binary, so changing the executable name cannot
// create a second catalog, executor or trust model.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := clicore.Run(ctx, standalone.RootFor("odoo-cli"), os.Args[1:], clicore.Streams{
		Out:         os.Stdout,
		Diagnostics: os.Stderr,
	})
	stop()
	os.Exit(code)
}
