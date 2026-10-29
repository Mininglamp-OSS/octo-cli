package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// TestXIN2453ToolSwitchUnicodeFoldRefused isolates the tool-switch rule: a
// spelling of dry_run or schema_fingerprint that strings.EqualFold matches
// (U+017F long s folds to "s") is refused like the ASCII spellings, while an
// operation that declares such a field as business input keeps it.
func TestXIN2453ToolSwitchUnicodeFoldRefused(t *testing.T) {
	body := map[string]registry.SchemaInfo{"name": {}}
	for _, k := range []string{"ſchema_fingerprint", "ſCHEMA_FINGERPRINT", "SCHEMA_FINGERPRINT", "Schema-FingerPrint", "ſchemaFingerprint", "DRY_RUN", "Dry-Run"} {
		err := checkArgumentNames(context.Background(), map[string]any{"name": "n", k: "x"}, map[string]bool{}, nil, nil, body)
		if err == nil || !strings.Contains(err.Error(), "is a tool option, not an operation argument") {
			t.Errorf("%q: err = %v, want the tool-option refusal", k, err)
		}
	}
	declared := map[string]registry.SchemaInfo{"schema_fingerprint": {}, "dry_run": {}}
	if err := checkArgumentNames(context.Background(), map[string]any{"schema_fingerprint": "x", "dry_run": true}, map[string]bool{}, nil, nil, declared); err != nil {
		t.Errorf("declared business fields were refused: %v", err)
	}
	// A declared field still does not admit a fold variant of itself.
	if err := checkArgumentNames(context.Background(), map[string]any{"ſchema_fingerprint": "x"}, map[string]bool{}, nil, nil, declared); err == nil {
		t.Errorf("a fold variant of a declared field was accepted")
	}
}

// TestXIN2453UnicodeNamesStayBounded keeps the fold key linear: the budget's
// worth of long Unicode names validates quickly, and an over-budget set is
// refused before any name is folded.
func TestXIN2453UnicodeNamesStayBounded(t *testing.T) {
	body := map[string]registry.SchemaInfo{"name": {}}
	name := func(i int) string { return strings.Repeat("ſK", 64) + extraName(i) }
	start := time.Now()
	if err := checkArgumentNames(context.Background(), manyArgs(maxArgumentNames, name), map[string]bool{}, nil, nil, body); err != nil {
		t.Fatalf("budget-sized Unicode names refused: %v", err)
	}
	if err := checkArgumentNames(context.Background(), manyArgs(30000, name), map[string]bool{}, nil, nil, body); err == nil || !strings.Contains(err.Error(), "too many arguments") {
		t.Fatalf("30000 names: err = %v, want the budget refusal", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("validation took %s", d)
	}
}
