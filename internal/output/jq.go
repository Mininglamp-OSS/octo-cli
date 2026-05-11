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
			return nil, fmt.Errorf("jq: %w", e)
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
