package mcp

import (
	"bytes"
	"context"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func TestVerifiedPackageFreezesSourceSlotsAndComparesRenderedSemantics(t *testing.T) {
	files, pin, baseline := retainedPackageFixture(t)
	alter := false
	var sourceOwned map[string]any
	var managedID string
	source := func(operation string, config permission.CatalogConfig, raw []byte, slots []permission.CatalogSlot) (map[string]any, error) {
		values, err := PermissionCatalogSlotValues(operation, config, raw, slots)
		if err == nil && sourceOwned == nil {
			for _, slot := range slots {
				if slot.Domain == "server_managed_argument_names" {
					sourceOwned, managedID = values, slot.ID
				}
			}
		}
		if err == nil && alter {
			for _, slot := range slots {
				if slot.Domain == "facade_hint" {
					values[slot.ID] = values[slot.ID].(string) + " fixture changed source"
				}
			}
		}
		return values, err
	}
	v, err := permission.VerifyCatalogPackage(context.Background(), files, pin, baseline, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sourceOwned[managedID].([]string); !ok {
		t.Fatal("verification mutated a source-owned slot assignment")
	}
	config := permission.CatalogConfig{ManagedMask: 1, Facade: 2}
	before, err := v.Schema("message.send", "describe_op", config)
	if err != nil {
		t.Fatal(err)
	}
	alter = true
	t.Run("frozen_snapshot", func(t *testing.T) {
		after, err := v.Schema("message.send", "describe_op", config)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("published snapshot consulted mutable source callback", err)
		}
	})
	t.Run("rendered_semantics", func(t *testing.T) {
		otherPin := commitPackageFixture(t, files, "fixture-changed-slot-source-v2")
		other, err := permission.VerifyCatalogPackage(context.Background(), files, otherPin, baseline, source)
		if err != nil {
			t.Fatal("valid structural source-domain fixture refused", err)
		}
		if v.SameSemantics(other) {
			t.Fatal("different rendered source values were declared semantically equivalent")
		}
	})
}
