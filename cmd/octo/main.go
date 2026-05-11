package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Mininglamp-OSS/octo-cli/cmd"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	f := cmdutil.NewDefaultFactory()
	root := cmd.NewRootCmd(f)
	if err := root.ExecuteContext(ctx); err != nil {
		// RunE paths emit an envelope via Factory.EmitError and set
		// f.ErrorEmitted. Only emit here for errors that bypassed the
		// Factory (cobra-framework errors, PersistentPreRunE failures).
		if !f.ErrorEmitted {
			wrapped := cmdutil.WrapCLIError(err)
			_ = output.WriteError(os.Stderr, wrapped) //nolint:errcheck // best-effort write before exit
		}

		// Determine exit code from the structured error.
		ee := output.AsExitError(cmdutil.WrapCLIError(err))
		if ee == nil {
			ee = output.ErrWithHint("internal", "INTERNAL", err.Error(), "")
		}
		return ee.ExitCode()
	}
	return 0
}
