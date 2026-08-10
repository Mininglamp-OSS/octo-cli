package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// enumNotAllowed is the error code for a value the spec's enum does not list.
// A distinct code (rather than the generic VALIDATION_ERROR) lets an agent
// branch on "wrong value from a closed set" without parsing the message, the
// same way TOKEN_KIND_NOT_ALLOWED distinguishes the credential gate.
const enumNotAllowed = "ENUM_NOT_ALLOWED"

// enumError reports a value outside a spec-declared enum. label is what the
// caller typed (`--role`, or a body path for a nested `--data` field), so the
// message points at the input rather than at the wire schema. The value is
// echoed because an agent that built it programmatically needs to see what
// actually arrived; enum-constrained fields are closed vocabularies, never
// secrets, so echoing is safe (x-octo-secret fields carry no enum).
func enumError(label string, value any, allowed []any) *output.ExitError {
	return output.ErrWithHint("validation", enumNotAllowed,
		fmt.Sprintf("%s: %v is not an accepted value", label, formatEnumValue(value)),
		fmt.Sprintf("pass one of: %s", formatEnum(allowed)))
}

// checkEnum reports an error when allowed is non-empty and value is not in it.
// An empty enum means "unconstrained" and passes everything.
//
// Comparison is by canonical form, not Go equality: the same wire value reaches
// this check as different Go types depending on how it was supplied — an
// integer body flag is an int, the same field via --data is a float64, a uint64
// id flag is a json.Number, and every spec enum entry is whatever
// encoding/json produced for the spec literal (float64 for numbers). Comparing
// with == would reject 1 against enum [1] for the --data path alone.
func checkEnum(label string, value any, allowed []any) *output.ExitError {
	if len(allowed) == 0 {
		return nil
	}
	got, ok := canonicalEnumValue(value)
	if !ok {
		// An object / array / null where the schema declares a scalar vocabulary
		// cannot match any member, so reject rather than forward. Forwarding is
		// what let `--data '{"im_channel_type":[1]}'` reach the backend and come
		// back as an internal decode error naming a server struct.
		return output.ErrWithHint("validation", enumNotAllowed,
			fmt.Sprintf("%s must be a single value from the accepted set, got %T", label, value),
			fmt.Sprintf("pass one of: %s", formatEnum(allowed)))
	}
	for _, a := range allowed {
		if want, ok := canonicalEnumValue(a); ok && want == got {
			return nil
		}
	}
	return enumError(label, value, allowed)
}

// canonicalEnumValue reduces a scalar to a type-tagged canonical string.
// The tag keeps the string "1" from matching the number 1, so a spec that
// declares a string enum still rejects a numeric --data value. Numbers compare by
// exact decimal text (see canonicalNumberText), so an integer vocabulary admits only
// values that are integers on the wire.
// Reports false for anything that is not a string, bool or number.
func canonicalEnumValue(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return "s:" + t, true
	case bool:
		return "b:" + strconv.FormatBool(t), true
	case int:
		return "n:" + strconv.FormatInt(int64(t), 10), true
	case int64:
		return "n:" + strconv.FormatInt(t, 10), true
	case uint64:
		return "n:" + strconv.FormatUint(t, 10), true
	case float64:
		return "n:" + canonicalNumberText(specNumberText(t)), true
	case json.Number:
		return "n:" + canonicalNumberText(t.String()), true
	}
	return "", false
}

// canonicalNumberText is the text a number is compared by: exactly what the
// caller wrote.
//
// An integral value written as a float compares equal to the same value written
// as an integer only because a spec literal is formatted back to integer text by
// specNumberText before it gets here — encoding/json hands over every JSON number
// as float64, so the entry `1` arrives as 1.0 and is rendered "1". What does NOT
// compare equal is a value that is not an integer on the wire: `1.0`, `1e0` and
// `1.00000000000000000001` are all distinct text from "1", which is the point.
// Collapsing them (as an earlier ParseFloat-and-truncate version did) let them
// pass the local gate and then travel verbatim, because the body keeps the
// caller's original json.Number — so a Go int field rejected them at the backend,
// reporting an internal struct name for a value the CLI had called valid.
func canonicalNumberText(s string) string {
	return s
}

// specNumberText renders a number that came from a spec literal. encoding/json
// decodes every JSON number as float64, so an integral vocabulary entry has to be
// formatted back to its integer text to be comparable with the caller's digits.
func specNumberText(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// formatEnumValue renders the rejected value for the error message: strings
// quoted so an empty or space-padded value is visible, everything else bare.
func formatEnumValue(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	return fmt.Sprintf("%v", v)
}

// enumFieldLabel names a body field the way the caller supplied it. A top-level
// property is settable as a promoted flag, so name the flag; a nested field can
// only come from --data, so name its JSON path.
func enumFieldLabel(path, flagName string) string {
	if flagName != "" {
		return "--" + flagName
	}
	if strings.ContainsAny(path, ".[") {
		return "--data field " + path
	}
	return "field " + path
}
