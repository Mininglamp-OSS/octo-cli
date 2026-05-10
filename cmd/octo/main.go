package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dmwork-org/octo-cli/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cmd.NewRootCmd(nil)
	if err := root.ExecuteContext(ctx); err != nil {
		// Error envelope has already been emitted by the command's RunE via
		// Factory.EmitError. Just set the exit code.
		os.Exit(1)
	}
}
