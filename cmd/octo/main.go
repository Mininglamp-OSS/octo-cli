package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dmwork-org/octo-cli/cmd"
	"github.com/dmwork-org/octo-cli/internal/cmdutil"
	"github.com/dmwork-org/octo-cli/internal/output"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cmd.NewRootCmd(nil)
	if err := root.ExecuteContext(ctx); err != nil {
		// RunE paths have already emitted an envelope via Factory.EmitError.
		// Cobra-framework errors (unknown flag, missing arg) and PersistentPreRunE
		// failures (missing token, bad --format) reach here as plain errors — wrap
		// and emit them so agents always get a structured envelope on stderr.
		wrapped := cmdutil.WrapCLIError(err)
		ee := output.AsExitError(wrapped)
		if ee == nil {
			ee = output.ErrWithHint("internal", "INTERNAL", err.Error(), "")
		}
		if output.AsExitError(err) == nil {
			// Only emit here for plain errors; RunE already emitted for *ExitError.
			_ = output.WriteError(os.Stderr, ee)
		}
		os.Exit(ee.ExitCode())
	}
}
