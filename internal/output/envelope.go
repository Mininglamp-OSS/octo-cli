package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// EnvelopeMeta carries optional envelope-level metadata that callers may attach
// to a success response. Fields are emitted with an underscore prefix to mark
// them as CLI-added (not backend data).
type EnvelopeMeta struct {
	RateLimit json.RawMessage
	Notice    json.RawMessage
}

// Identity is the actor identity tag emitted on successful responses. Phase 1
// is bot-only; kept as a constant for now, may become a field later.
const Identity = "bot"

// WriteSuccess emits a success envelope to w. If raw is an object containing
// top-level "data" (array) + "pagination" (object) keys, those are flattened
// onto the envelope as data + _pagination. Otherwise raw is placed under data
// as-is. Meta fields (if non-nil) are attached as _rate_limit / _notice.
func WriteSuccess(w io.Writer, raw json.RawMessage, meta EnvelopeMeta) error {
	env := map[string]any{
		"ok":       true,
		"identity": Identity,
	}

	dataField, paginationField, ok := splitPagination(raw)
	if ok {
		env["data"] = dataField
		env["_pagination"] = paginationField
	} else if len(raw) == 0 {
		env["data"] = nil
	} else {
		env["data"] = raw
	}

	if len(meta.RateLimit) > 0 {
		env["_rate_limit"] = meta.RateLimit
	}
	if len(meta.Notice) > 0 {
		env["_notice"] = meta.Notice
	}

	return writeJSON(w, env)
}

// WriteError emits an error envelope to w. err is rendered as the envelope's
// error object; non-ExitError values are wrapped as a generic internal error.
func WriteError(w io.Writer, err error) error {
	ee := AsExitError(err)
	if ee == nil {
		ee = &ExitError{
			Type:    "internal",
			Code:    "INTERNAL",
			Message: errorMessage(err),
		}
	}

	errObj := map[string]any{
		"type":    ee.Type,
		"code":    ee.Code,
		"message": ee.Message,
	}
	if ee.Hint != "" {
		errObj["hint"] = ee.Hint
	}
	if len(ee.Detail) > 0 {
		errObj["detail"] = ee.Detail
	}

	return writeJSON(w, map[string]any{
		"ok":    false,
		"error": errObj,
	})
}

// splitPagination detects the backend's paginated shape and splits it into a
// data value (RawMessage of the array) and a pagination value. Returns ok=false
// if raw is not an object with those two keys.
func splitPagination(raw json.RawMessage) (data json.RawMessage, pagination json.RawMessage, ok bool) {
	if len(raw) == 0 {
		return nil, nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, nil, false
	}
	d, hasData := obj["data"]
	p, hasPag := obj["pagination"]
	if !hasData || !hasPag {
		return nil, nil, false
	}
	if !isJSONArray(d) {
		return nil, nil, false
	}
	if !isJSONObject(p) {
		return nil, nil, false
	}
	return d, p, true
}

func isJSONArray(raw json.RawMessage) bool {
	for _, b := range raw {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		return b == '['
	}
	return false
}

func isJSONObject(raw json.RawMessage) bool {
	for _, b := range raw {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		return b == '{'
	}
	return false
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func writeJSON(w io.Writer, v any) error {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	buf = append(buf, '\n')
	_, werr := w.Write(buf)
	return werr
}
