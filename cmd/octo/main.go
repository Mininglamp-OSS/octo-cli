package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dmwork-org/octo-cli/cmd"
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
		ee := output.AsExitError(err)
		if ee == nil {
			ee = classifyCLIError(err)
			_ = output.WriteError(os.Stderr, ee)
		}
		os.Exit(ee.ExitCode())
	}
}

// classifyCLIError maps a plain error from cobra or PersistentPreRunE into a
// structured *ExitError. Heuristics stay intentionally narrow — the common
// agent-facing failures (missing token, unknown flag, missing arg) map to
// their real taxonomy; everything else falls through to a generic config
// error so exit codes and envelopes stay predictable.
func classifyCLIError(err error) *output.ExitError {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "octo_bot_token"),
		strings.Contains(lower, "bot token"),
		strings.Contains(lower, "token is required"):
		return output.ErrAuth(msg, "set OCTO_BOT_TOKEN to an app_* or bf_* token")
	case strings.Contains(lower, "unknown flag"),
		strings.Contains(lower, "unknown command"),
		strings.Contains(lower, "unknown shorthand"),
		strings.Contains(lower, "required flag"),
		strings.Contains(lower, "invalid argument"),
		strings.Contains(lower, "accepts "),
		strings.Contains(lower, "requires at "),
		strings.Contains(lower, "arg(s)"):
		return output.ErrValidation(msg, "run `octo <command> --help` to see valid flags and args")
	}
	return output.ErrWithHint("config", "CLI_ERROR", msg, "")
}
