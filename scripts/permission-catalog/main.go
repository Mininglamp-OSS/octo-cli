// Command permission-catalog materializes pinned source content offline. Its
// output is deliberately not a supported permission release or allowlist.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mininglamp-OSS/octo-cli/internal/mcp"
	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--slot-witness" {
		raw, err := mcp.MaterializePermissionSlotWitness(context.Background(), permission.CatalogPin{Version: "octo-capabilities-v1-xin2748-v4", Digest: os.Args[2]})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if _, err := os.Stdout.Write(raw); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--package-output" {
		if err := writePackage(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	var b []byte
	var err error
	if len(os.Args) > 1 && os.Args[1] == "--release-report" {
		b, err = mcp.AssemblePermissionRelease()
	} else if len(os.Args) > 1 && os.Args[1] == "--dynamic" {
		b, err = mcp.MaterializePermissionContent()
	} else {
		b, err = mcp.MaterializePermissionSources()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err = os.Stdout.Write(b); err != nil {
		os.Exit(1)
	}
}

func writePackage(dir string) error {
	p, err := mcp.MaterializePermissionPackage(context.Background())
	if err != nil {
		return err
	}
	baseline, err := mcp.PermissionCatalogBaseline()
	if err != nil {
		return err
	}
	verified, err := permission.VerifyCatalogPackage(context.Background(), p.Files, p.Pin, baseline, mcp.PermissionCatalogSlotValues)
	if err != nil {
		return err
	}
	if verified.Ledger() != p.Ledger {
		return fmt.Errorf("independent producer/consumer ledgers disagree")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	inventory := map[string]any{}
	total := 0
	for path, raw := range p.Files {
		target := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(raw)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		total += len(raw)
		inventory[path] = len(raw)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "VERIFIED_CANDIDATE_NOT_ALLOWLISTED", "manifest_digest": p.Pin.Digest, "stored_bytes": total, "whole_resource_stored_bytes": p.WholeStoredBytes, "factoring_threshold": p.FactoringThreshold, "source_conformance": p.Conformance, "files": inventory, "ledger": verified.Ledger(), "semantic_sha256": verified.SemanticDigest()})
}
