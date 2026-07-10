package service

import (
	"fmt"

	"github.com/Mininglamp-OSS/octo-cli/internal/fracindex"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// indexHint is the guidance attached to every fractional-index validation
// error so a caller (typically an agent) learns the correct way to produce a
// key instead of inventing one.
const indexHint = "every whiteboard element must carry a valid fractional-index `index` (z-order key) — generate it with the fractional-indexing / Excalidraw rules (e.g. \"a0\", \"a1\"); never omit it or fabricate an \"r\"+digits, plain-integer, or timestamp value"

// validateElementsIndex enforces that every element in a scene-edit body carries
// a valid fractional-index `index`. It is invoked only for operations that
// declare x-octo-validate-elements-index in the spec. Rejecting here — before
// the request is sent — stops index-less / garbage-index elements from reaching
// the backend, where a buggy repair path would rewrite them into an invalid key
// (XIN-792) and break the board.
//
// A body with no `elements` key (e.g. a delete-only batch) is left untouched.
func validateElementsIndex(body any) *output.ExitError {
	m, ok := body.(map[string]any)
	if !ok {
		// No structured body (or --data was not an object); nothing to check.
		return nil
	}
	raw, present := m["elements"]
	if !present {
		return nil
	}
	elems, ok := raw.([]any)
	if !ok {
		return output.ErrValidation("`elements` must be a JSON array", indexHint)
	}
	for i, e := range elems {
		el, ok := e.(map[string]any)
		if !ok {
			return output.ErrValidation(fmt.Sprintf("elements[%d] must be a JSON object", i), indexHint)
		}
		label := elementLabel(el, i)
		idxRaw, has := el["index"]
		if !has {
			return output.ErrValidation(fmt.Sprintf("%s is missing `index`", label), indexHint)
		}
		idx, ok := idxRaw.(string)
		if !ok {
			return output.ErrValidation(fmt.Sprintf("%s `index` must be a string", label), indexHint)
		}
		if err := fracindex.ValidateOrderKey(idx); err != nil {
			return output.ErrValidation(fmt.Sprintf("%s has an invalid `index`: %v", label, err), indexHint)
		}
	}
	return nil
}

// elementLabel builds a human-readable reference to an element for error
// messages, preferring its id when present.
func elementLabel(el map[string]any, i int) string {
	if id, ok := el["id"].(string); ok && id != "" {
		return fmt.Sprintf("elements[%d] (id %q)", i, id)
	}
	return fmt.Sprintf("elements[%d]", i)
}
