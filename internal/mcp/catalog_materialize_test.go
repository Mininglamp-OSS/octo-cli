package mcp

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPermissionSourceMaterializationCoverageAndContent(t *testing.T) {
	raw, err := MaterializePermissionSources()
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Counts        map[string]int         `json:"counts"`
		CatalogDigest any                    `json:"catalog_digest"`
		Entries       []materializedEntry    `json:"entries"`
		Blobs         map[string]contentBlob `json:"blobs"`
	}
	if err = json.Unmarshal(raw, &artifact); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]int{"entries": 827, "operation_truths": 393, "base_facade_variants": 1179, "filtered_resources": 24} {
		if artifact.Counts[k] != want {
			t.Fatalf("%s=%d want %d", k, artifact.Counts[k], want)
		}
	}
	if artifact.CatalogDigest != nil {
		t.Fatal("partial artifact masquerades as release")
	}
	for sum, blob := range artifact.Blobs {
		b, e := base64.StdEncoding.DecodeString(blob.Base64)
		if e != nil || len(b) != blob.Bytes || sha256Sum(b) != sum {
			t.Fatal("bad blob", sum)
		}
	}
	for _, e := range artifact.Entries {
		for _, file := range e.SourceFiles {
			if _, err = os.Stat(filepath.Join("..", "..", file)); err != nil {
				t.Fatal(e.ID, "bad source reference", file, err)
			}
		}
		if e.ManagedStatus != "NOT_WIRED" || len(e.Remaining) == 0 {
			t.Fatal("unproved reachability hidden", e.ID)
		}
		refs := []string{e.Truth}
		for _, sum := range e.Variants {
			refs = append(refs, sum)
		}
		for _, sum := range refs {
			if sum != "" {
				if _, ok := artifact.Blobs[sum]; !ok {
					t.Fatal("missing reference", e.ID, sum)
				}
			}
		}
	}
	// The independently retained exact source artifact makes content changes
	// reviewable across registry/handler/skill edits, not merely count changes.
	f, err := os.Open("../../docs/permission/source-materialization-v1.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	golden, err := io.ReadAll(io.LimitReader(g, 8<<20))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, golden) {
		t.Fatal("materialized content drift; regenerate and inspect the source artifact")
	}
}
