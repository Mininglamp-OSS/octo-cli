package mcp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func TestPermissionPackageV2CompleteRoundTrip(t *testing.T) {
	production, err := MaterializePermissionPackage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if packageStored(production.Files) > 8388608 {
		t.Fatalf("produced v2 package exceeds approved stored8MiB: %d", packageStored(production.Files))
	}
	baseline, err := PermissionCatalogBaseline()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := permission.VerifyCatalogPackage(context.Background(), production.Files, production.Pin, baseline, PermissionCatalogSlotValues)
	if err != nil {
		t.Fatal("complete approved v2 package could not be independently loaded", err)
	}
	if len(snapshot.Catalog().Capabilities) != 827 || snapshot.Ledger().SchemaCells != 56592 || snapshot.Ledger().DiscoveryCells != 156 || snapshot.Ledger().StaticCells != 1248 {
		t.Fatal("incomplete package inventory or selector partition", snapshot.Ledger())
	}
	if snapshot.Ledger() != production.Ledger {
		t.Fatal("independent source-producer and consumer ledgers disagree", production.Ledger, snapshot.Ledger())
	}
	if snapshot.Ledger().StoredBytes > 8388608 || snapshot.Ledger().PreBytes > 16777216 || snapshot.Ledger().PostBytes > 16777216 {
		t.Fatal("package exceeded approved accounting ceilings", snapshot.Ledger())
	}
	t.Run("source_rendering", func(t *testing.T) {
		counts, err := ComparePermissionPackageSource(context.Background(), snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if counts["schema_cells"] != 56592 || counts["static_cells"] != 1248 || counts["discovery_source_observations"] != 1248 {
			t.Fatal(counts)
		}
	})
	t.Run("private_control_http_stdio", func(t *testing.T) {
		f := managedPackageFixture(t, snapshot)
		runManagedPrivateControlToHTTPRelay(t, f)
	})
	t.Run("cached_resource_fresh_native_access", func(t *testing.T) {
		f := managedPackageFixture(t, snapshot)
		turn := f.turn()
		defer turn.Close()
		s := f.server(turn)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		s.Dispatch(ctx, initializedRequest())
		req := rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`2`), Method: "resources/read", Params: json.RawMessage(`{"uri":"octo://skills/octo-messaging/SKILL.md#anchor"}`)}
		if err := relayDeclaration(&req, turn.source); err != nil {
			t.Fatal(err)
		}
		if r, _ := s.Dispatch(ctx, req); r.Error != nil {
			t.Fatal("supported cached resource refused", r)
		}
		reads := f.nativeReads.Load()
		f.eligible.Store(false)
		if r, _ := s.Dispatch(ctx, req); r.Error == nil || r.Error.Code != -32007 || f.nativeReads.Load() <= reads || f.bundle.Revision != "0" || f.business.Load() != 0 {
			t.Fatal("cached resource bypassed fresh native revocation with unchanged policy", r)
		}
	})
	t.Run("latest_endpoint_actual_executable", func(t *testing.T) {
		path := os.Getenv("XIN2794_ENDPOINT_V43_CONTROL_SOURCE")
		if path == "" {
			t.Skip("exact endpoint afb732b source not supplied")
		}
		source, err := os.ReadFile(path)
		if err != nil || sha256Sum(source) != "4e40ee144b5eec7ef645be92948dd6b1f36a838d1fb82e6b471a59e820c99bef" {
			t.Fatal("endpoint v43 source mismatch", err)
		}
		runPinnedEndpointControlAndActualTurnRelayExecutable(t, managedPackageFixture(t, snapshot), source)
	})
}

func managedPackageFixture(t *testing.T, v *permission.VerifiedCatalog) *managedFixture {
	t.Helper()
	f := newManagedFixture(t)
	f.bundle.CatalogVersion = v.Pin().Version
	f.bundle.CatalogDigest = v.Pin().Digest
	caps := f.bundle.Capability.Rules[0]["capabilities"].([]string)
	f.bundle.Capability.Rules[0]["capabilities"] = append(caps, "octo-cli/discovery/search-all", "octo-cli/message/discovery/search")
	f.save()
	var err error
	f.coordinator, err = permission.NewCoordinator(v.Catalog(), f.policies, f.reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.coordinator.ConfigureCatalogPackage(v); err != nil {
		t.Fatal(err)
	}
	bounds, err := PermissionCatalogPageBounds()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.coordinator.ConfigurePageBounds(bounds); err != nil {
		t.Fatal(err)
	}
	return f
}
