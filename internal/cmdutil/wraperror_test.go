package cmdutil

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// Round-13 P1-a. WrapCLIError passed pflag's and cobra's own text straight into the
// envelope, and five of those formats embed the caller's argv verbatim:
//
//	unknown flag: --%s                        <- the text after "--"
//	unknown shorthand flag: %q in -%s         <- the whole "-Ab3..." run
//	flag needs an argument: %q in -%s         <- same run
//	invalid argument %q for %q flag: %v       <- the value
//	unknown command %q for %q                 <- the token
//
// A base64url share or invite token starts with "-" often enough that the
// x-octo-flag escape hatch exists for it, so a missing verb or a mistyped flag name
// put the token into the structured error on stderr — unconditionally, with no
// --verbose. This path runs before collectSecrets, so there is no secret list to mask
// against: the only available fix is to stop echoing the fragment.
func TestWrapCLIError_NeverEchoesArgv(t *testing.T) {
	const token = "Ab3cDeFgHiJkLmN0pQrS"

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"unknown shorthand run", errors.New("unknown shorthand flag: 'A' in -" + token)},
		{"unknown long flag", errors.New("unknown flag: --" + token)},
		{"flag needs an argument, shorthand", errors.New("flag needs an argument: 'A' in -" + token)},
		{"invalid argument for a flag", errors.New(`invalid argument "` + token + `" for "--share-id" flag: bad`)},
		{"unknown command", errors.New(`unknown command "` + token + `" for "octo-cli drive"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ee := output.AsExitError(WrapCLIError(tc.err))
			if ee == nil {
				t.Fatalf("expected a structured error, got %v", tc.err)
			}
			visible := ee.Message + " " + ee.Hint + " " + string(ee.Detail)
			if strings.Contains(visible, token) {
				t.Errorf("the argv fragment reached the envelope: %s", visible)
			}
			if ee.Type != "validation" {
				t.Errorf("Type = %q, want validation", ee.Type)
			}
			// Still actionable: the caller has to learn what to do next.
			if ee.Hint == "" {
				t.Error("dropping the text must not also drop the remedy")
			}
		})
	}
}

// TestWrapCLIError_KeepsValueFreeDiagnostics is the other direction. Not every message
// in that switch embeds argv, and blanking the useful ones would trade one defect for
// another: an arg-count error carries only numbers, and cobra's required-flag error
// carries only flag names, which is the actionable part.
func TestWrapCLIError_KeepsValueFreeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		err        error
	}{
		{"arg count", "accepts 1 arg(s), received 3", errors.New("accepts 1 arg(s), received 3")},
		{"minimum args", "requires at least 1 arg(s), only received 0", errors.New("requires at least 1 arg(s), only received 0")},
		{"required flag names", `required flag(s) "space-id" not set`, errors.New(`required flag(s) "space-id" not set`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ee := output.AsExitError(WrapCLIError(tc.err))
			if ee == nil {
				t.Fatalf("expected a structured error, got %v", tc.err)
			}
			if !strings.Contains(ee.Message, tc.want) {
				t.Errorf("message = %q, want it to keep %q — these carry counts and flag names, not caller values",
					ee.Message, tc.want)
			}
		})
	}
}

// TestWrapCLIError_AuthMessagesAreOurOwnText keeps the auth branch intact: those
// strings are produced by this project's config and credential code, not by pflag, so
// they cannot carry argv and they name the variable to set.
func TestWrapCLIError_AuthMessagesAreOurOwnText(t *testing.T) {
	ee := output.AsExitError(WrapCLIError(errors.New("bot token is required")))
	if ee == nil || ee.Type != "auth_error" {
		t.Fatalf("got %v, want an auth error", ee)
	}
	if !strings.Contains(ee.Message, "token is required") {
		t.Errorf("message = %q, want the original diagnostic", ee.Message)
	}
}
