package cmd

import (
	"encoding/json"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
)

// sheet-cell is offline (no token) and must emit a Univer cell
// {v, s:{n:{pattern}}, t:2} for each value source.
func TestCmd_SheetCell(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantV       float64
		wantPattern string
	}{
		{"date", []string{"sheet-cell", "--date", "2025-01-10"}, 45667, "yyyy-mm-dd"},
		{"datetime", []string{"sheet-cell", "--datetime", "2025-01-10 12:00"}, 45667.5, "yyyy-mm-dd hh:mm"},
		{"percent", []string{"sheet-cell", "--percent", "25"}, 0.25, "0%"},
		{"currency", []string{"sheet-cell", "--currency", "1200"}, 1200, "¥#,##0.00"},
		{"currency-symbol", []string{"sheet-cell", "--currency", "1200", "--symbol", "$"}, 1200, "$#,##0.00"},
		{"thousands", []string{"sheet-cell", "--thousands", "1234567"}, 1234567, "#,##0"},
		{"number-pattern", []string{"sheet-cell", "--number", "3.14", "--pattern", "0.00"}, 3.14, "0.00"},
		{"pattern-override", []string{"sheet-cell", "--date", "2025-01-10", "--pattern", "yyyy/m/d"}, 45667, "yyyy/m/d"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newTestFactoryWithReg()
			f.SetConfig(&config.Config{Format: "json"})
			out, _, err := execRoot(t, f, c.args...)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			var env map[string]any
			if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
				t.Fatalf("unmarshal: %v\n%s", jerr, out)
			}
			if env["ok"] != true {
				t.Fatalf("ok = %v\n%s", env["ok"], out)
			}
			data, _ := env["data"].(map[string]any)
			if v, _ := data["v"].(float64); v != c.wantV {
				t.Errorf("v = %v, want %v", data["v"], c.wantV)
			}
			if tt, _ := data["t"].(float64); tt != 2 {
				t.Errorf("t = %v, want 2", data["t"])
			}
			s, _ := data["s"].(map[string]any)
			n, _ := s["n"].(map[string]any)
			if n["pattern"] != c.wantPattern {
				t.Errorf("pattern = %v, want %v", n["pattern"], c.wantPattern)
			}
		})
	}
}

// A plain --number with no --pattern is a bare numeric cell (no style).
func TestCmd_SheetCellPlainNumber(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})
	out, _, err := execRoot(t, f, "sheet-cell", "--number", "82")
	if err != nil {
		t.Fatalf("sheet-cell: %v", err)
	}
	var env map[string]any
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("unmarshal: %v\n%s", jerr, out)
	}
	data, _ := env["data"].(map[string]any)
	if v, _ := data["v"].(float64); v != 82 {
		t.Errorf("v = %v, want 82", data["v"])
	}
	if _, hasStyle := data["s"]; hasStyle {
		t.Errorf("plain number should carry no style, got %v", data["s"])
	}
}

func TestCmd_SheetCellErrors(t *testing.T) {
	cases := [][]string{
		{"sheet-cell"}, // no source
		{"sheet-cell", "--date", "2025-01-10", "--percent", "25"}, // two sources
		{"sheet-cell", "--date", "notadate"},                      // bad date
		{"sheet-cell", "--percent", "abc"},                        // bad number
		{"sheet-cell", "--datetime", "2025-01-10"},                // missing time
	}
	for _, args := range cases {
		f := newTestFactoryWithReg()
		f.SetConfig(&config.Config{Format: "json"})
		_, errOut, err := execRoot(t, f, args...)
		if err == nil {
			t.Errorf("%v: expected error, got none", args)
			continue
		}
		var env map[string]any
		if jerr := json.Unmarshal([]byte(errOut), &env); jerr != nil {
			t.Errorf("%v: unmarshal errOut: %v\n%s", args, jerr, errOut)
			continue
		}
		if env["ok"] != false {
			t.Errorf("%v: ok = %v, want false", args, env["ok"])
		}
	}
}
