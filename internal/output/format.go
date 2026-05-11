package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// Supported output formats.
const (
	FormatJSON   = "json"
	FormatTable  = "table"
	FormatCSV    = "csv"
	FormatNDJSON = "ndjson"
)

// Format renders v to w using the requested format. For non-JSON formats v is
// expected to be a JSON-serializable value, typically the success envelope (a
// map) or a plain slice / object. When the format can't represent v sensibly
// (e.g. table on a scalar), it falls back to JSON.
func Format(w io.Writer, format string, v any) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", FormatJSON:
		return writeJSON(w, v)
	case FormatTable:
		return writeTable(w, v)
	case FormatCSV:
		return writeCSV(w, v)
	case FormatNDJSON:
		return writeNDJSON(w, v)
	default:
		return fmt.Errorf("unknown format %q (expected json|table|csv|ndjson)", format)
	}
}

// extractRows returns the rows the tabular formatters (table, csv) should
// render. It unwraps a success envelope to its data field and coerces scalar
// objects to a single-row slice. Returns (nil, false) for values that cannot
// be rendered as rows.
func extractRows(v any) ([]map[string]any, bool) {
	buf, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}

	// Try envelope-wrapped data first.
	var env struct {
		OK   *bool           `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(buf, &env) == nil && env.OK != nil && len(env.Data) > 0 {
		return coerceRows(env.Data)
	}
	return coerceRows(buf)
}

func coerceRows(raw json.RawMessage) ([]map[string]any, bool) {
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, true
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		return []map[string]any{obj}, true
	}
	return nil, false
}

func writeTable(w io.Writer, v any) error {
	rows, ok := extractRows(v)
	if !ok {
		return writeJSON(w, v)
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no results)")
		return err
	}
	headers := tableHeaders(rows)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	fmt.Fprintln(tw, strings.Join(repeat("---", len(headers)), "\t"))
	for _, row := range rows {
		cols := make([]string, len(headers))
		for i, h := range headers {
			cols[i] = renderCell(row[h])
		}
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
	}
	return tw.Flush()
}

func writeCSV(w io.Writer, v any) error {
	rows, ok := extractRows(v)
	if !ok {
		return writeJSON(w, v)
	}
	if len(rows) == 0 {
		return nil
	}
	headers := tableHeaders(rows)
	cw := csv.NewWriter(w)
	if err := cw.Write(headers); err != nil {
		return err
	}
	for _, row := range rows {
		record := make([]string, len(headers))
		for i, h := range headers {
			record[i] = renderCell(row[h])
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeNDJSON(w io.Writer, v any) error {
	rows, ok := extractRows(v)
	if !ok {
		return writeJSON(w, v)
	}
	enc := json.NewEncoder(w)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

// tableHeaders picks a deterministic column set. When common todo/matter keys
// are present they are ordered first; remaining keys are appended alphabetically.
func tableHeaders(rows []map[string]any) []string {
	priority := []string{"id", "title", "status", "assignee_id", "creator_id", "goal_id", "deadline", "created_at", "updated_at"}
	seen := map[string]bool{}
	var headers []string

	present := map[string]bool{}
	for _, r := range rows {
		for k := range r {
			present[k] = true
		}
	}

	for _, h := range priority {
		if present[h] && !seen[h] {
			headers = append(headers, h)
			seen[h] = true
		}
	}

	var rest []string
	for k := range present {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	headers = append(headers, rest...)
	return headers
}

func renderCell(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case []any, map[string]any:
		b, _ := json.Marshal(val) //nolint:errcheck // val is pre-validated JSON-safe type
		return string(b)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}
