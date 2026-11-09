package mcp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func TestSourceSlotWitnessMatchesEveryConsumerCell(t *testing.T) {
	files, pin, baseline := retainedPackageFixture(t)
	raw, err := MaterializePermissionSlotWitness(context.Background(), pin)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../../docs/permission/source-slot-witness-v5.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := io.ReadAll(z)
	if err != nil || !bytes.Equal(retained, raw) {
		t.Fatal("source slot witness is not reproducible", err)
	}
	witness := packageTestObject(t, raw)
	assignments := map[string]map[string]any{}
	key := func(entry, facade string, r, mask, f int) string {
		return fmt.Sprintf("%s|%s|%d|%d|%d", entry, facade, r, mask, f)
	}
	each := func(selector map[string]any, visit func(int, int, int)) {
		for r := 0; r < 2; r++ {
			for mask := 0; mask < 8; mask++ {
				for f := 0; f < 3; f++ {
					if fixtureInteger(selector["resources_bits"])&(1<<r) != 0 && fixtureInteger(selector["managed_bits"])&(1<<mask) != 0 && fixtureInteger(selector["facade_bits"])&(1<<f) != 0 {
						visit(r, mask, f)
					}
				}
			}
		}
	}
	for _, item := range witness["groups"].([]any) {
		g := item.(map[string]any)
		each(g["selector"].(map[string]any), func(r, mask, f int) {
			k := key(g["entry_id"].(string), g["facade"].(string), r, mask, f)
			if assignments[k] != nil {
				t.Fatal("source slot witness overlap", k)
			}
			assignments[k] = g["values"].(map[string]any)
		})
	}
	if len(assignments) != 29070 || fixtureInteger(witness["schema_source_cells"]) != 56592 {
		t.Fatal("source slot witness coverage drift")
	}
	content, roots := map[string]map[string]any{}, map[string][]byte{}
	variants := []map[string]any{}
	for path, raw := range files {
		if len(path) < 7 || path[:7] != "shared/" && path[:7] != "modules" {
			continue
		}
		m := packageTestObject(t, raw)
		if items, ok := m["content"].([]any); ok {
			for _, item := range items {
				c := item.(map[string]any)
				if hash, ok := c["sha256"].(string); ok {
					content[hash] = c
				} else if c["kind"] == "schema_variant" {
					variants = append(variants, c)
				}
			}
		}
		if items, ok := m["roots"].([]any); ok {
			for _, item := range items {
				r := item.(map[string]any)
				for _, item := range r["parts"].([]any) {
					p := item.(map[string]any)
					at, n := fixtureInteger(p["offset"]), fixtureInteger(p["length"])
					roots[r["sha256"].(string)] = append(roots[r["sha256"].(string)], files[p["path"].(string)][at:at+n]...)
				}
			}
		}
	}
	entries := map[string]string{}
	for i := range baseline.Entries {
		e := &baseline.Entries[i]
		if e.Kind != "schema" {
			continue
		}
		var target []string
		if err := json.Unmarshal(e.Target, &target); err != nil {
			t.Fatal(err)
		}
		entries[target[1]] = e.ID
	}
	checked := 0
	for _, c := range variants {
		operation := c["operation_id"].(string)
		template := content[c["template_hash"].(string)]
		var slots []permission.CatalogSlot
		if err := json.Unmarshal(catalogCanonicalJSON(template["slots"]), &slots); err != nil {
			t.Fatal(err)
		}
		if len(slots) == 0 {
			continue
		}
		each(c["selector"].(map[string]any), func(r, mask, f int) {
			values, err := PermissionCatalogSlotValues(operation, permission.CatalogConfig{Resources: r == 1, ManagedMask: mask, Facade: f}, roots[template["value_ref"].(string)], slots)
			if err != nil {
				t.Fatal(err)
			}
			expected := assignments[key(entries[operation], c["facade"].(string), r, mask, f)]
			if len(expected) != len(slots) {
				t.Fatal("missing/excess joint source assignment")
			}
			for _, slot := range slots {
				if !bytes.Equal(catalogCanonicalJSON(values[slot.ID]), catalogCanonicalJSON(expected[slot.Domain])) {
					t.Fatal("consumer/source joint slot mismatch")
				}
			}
			checked++
		})
	}
	if checked != len(assignments) {
		t.Fatal("source witness has missing or extra package cells", checked)
	}
}
