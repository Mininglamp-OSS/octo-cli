package output

import (
	"encoding/json"
	"fmt"

	"github.com/itchyny/gojq"
)

// ApplyJQ runs a jq expression against v and returns the resulting values.
// When the query yields a single result, the first (and only) element of the
// returned slice holds it; when it yields multiple, all are returned in order.
// An empty query is a passthrough.
func ApplyJQ(v any, expr string) ([]any, error) {
	if expr == "" {
		return []any{v}, nil
	}

	query, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("parse jq expression: %w", err)
	}

	input, err := normalizeForJQ(v)
	if err != nil {
		return nil, fmt.Errorf("normalize input: %w", err)
	}

	iter := query.Run(input)
	var results []any
	for {
		r, ok := iter.Next()
		if !ok {
			break
		}
		if e, ok := r.(error); ok {
			// gojq's runtime errors quote the value they choked on, and that value is
			// *response data* — which on a share command is the share token itself. So
			// `--jq '.data.share_token|tonumber'` printed the token on stderr while
			// stdout stayed empty: a caller narrowing their output with --jq got the
			// secret instead. Nothing here has a secret list to mask against (this runs
			// in the output layer, below the client), so the value is not repeated.
			//
			// This is also why it must be an *ExitError: as a plain error it fell to
			// WrapCLIError's generic fallback, which both echoed the text and filed it
			// as a config error rather than a validation one.
			_ = e
			return nil, ErrWithHint("validation", "JQ_RUNTIME_ERROR",
				"the --jq program failed while evaluating the response",
				"check the filter against the unfiltered output first; the value it failed on is "+
					"not repeated here because response data can itself be a secret")
		}
		results = append(results, r)
	}
	return results, nil
}

// normalizeForJQ round-trips v through JSON so gojq receives a canonical
// any tree (map[string]any / []any / primitives). Saves us from dealing with
// json.RawMessage / custom types inside the jq engine.
func normalizeForJQ(v any) (any, error) {
	// Fast path: already a canonical shape and not a RawMessage.
	if _, isRaw := v.(json.RawMessage); !isRaw {
		switch v.(type) {
		case map[string]any, []any, string, float64, bool, nil:
			return v, nil
		}
	}

	buf, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, err
	}
	return out, nil
}
