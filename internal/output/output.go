package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// Print outputs data in the specified format.
func Print(format string, data json.RawMessage) {
	switch format {
	case "table":
		printTable(data)
	default:
		printJSON(data)
	}
}

func printJSON(data json.RawMessage) {
	var buf json.RawMessage
	if err := json.Unmarshal(data, &buf); err != nil {
		fmt.Fprintln(os.Stdout, string(data))
		return
	}
	pretty, err := json.MarshalIndent(buf, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stdout, string(data))
		return
	}
	fmt.Fprintln(os.Stdout, string(pretty))
}

func printTable(data json.RawMessage) {
	// Try to parse as array of objects
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		// Try single object
		var item map[string]any
		if err := json.Unmarshal(data, &item); err != nil {
			printJSON(data)
			return
		}
		items = []map[string]any{item}
	}

	if len(items) == 0 {
		fmt.Println("No results.")
		return
	}

	// Collect column headers from first item
	headers := collectHeaders(items[0])
	if len(headers) == 0 {
		printJSON(data)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	// Print header
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Repeat("---\t", len(headers)))

	// Print rows
	for _, item := range items {
		var cols []string
		for _, h := range headers {
			v := item[h]
			cols = append(cols, formatValue(v))
		}
		fmt.Fprintln(w, strings.Join(cols, "\t"))
	}
	w.Flush()
}

func collectHeaders(item map[string]any) []string {
	// Priority headers for todo items
	priority := []string{"id", "title", "status", "creator_id", "goal_id", "deadline", "created_at"}
	var result []string
	for _, h := range priority {
		if _, ok := item[h]; ok {
			result = append(result, h)
		}
	}
	return result
}

func formatValue(v any) string {
	if v == nil {
		return "-"
	}
	switch val := v.(type) {
	case string:
		if len(val) > 36 {
			return val[:33] + "..."
		}
		return val
	case float64:
		return fmt.Sprintf("%.0f", val)
	case bool:
		if val {
			return "yes"
		}
		return "no"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// PrintError prints an error message to stderr as JSON.
func PrintError(err error) {
	msg, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintln(os.Stderr, string(msg))
}
