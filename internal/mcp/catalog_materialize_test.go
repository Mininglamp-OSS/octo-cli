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
	compressed, err := os.ReadFile("../../docs/permission/source-materialization-v1.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	compressedReader := bytes.NewReader(compressed)
	g, err := gzip.NewReader(compressedReader)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.Multistream(false)
	golden, err := io.ReadAll(io.LimitReader(g, (8<<20)+1))
	if err != nil {
		t.Fatal(err)
	}
	if len(golden) > 8<<20 {
		t.Fatal("materialized artifact exceeds its complete-read bound")
	}
	if compressedReader.Len() != 0 {
		t.Fatal("trailing compressed catalog data")
	}
	if !bytes.Equal(raw, golden) {
		t.Fatal("materialized content drift; regenerate and inspect the source artifact")
	}
}

func TestPermissionContinuationArtifactsMatchCompleteRendering(t *testing.T) {
	manifestBytes, err := os.ReadFile("../../docs/permission/contract-manifest-v4.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Files map[string]struct {
			Bytes      int    `json:"bytes"`
			SHA        string `json:"sha256"`
			Decoded    int    `json:"decoded_bytes"`
			DecodedSHA string `json:"decoded_sha256"`
		} `json:"files"`
	}
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	decoded := map[string][]byte{}
	for path, entry := range manifest.Files {
		b, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatal(path, err)
		}
		if len(b) != entry.Bytes || sha256Sum(b) != entry.SHA {
			t.Fatal("artifact bytes changed", path)
		}
		if entry.Decoded == 0 {
			continue
		}
		compressed := bytes.NewReader(b)
		reader, err := gzip.NewReader(compressed)
		if err != nil {
			t.Fatal(err)
		}
		reader.Multistream(false)
		raw, err := io.ReadAll(io.LimitReader(reader, (32<<20)+1))
		reader.Close()
		if err != nil || len(raw) != entry.Decoded || sha256Sum(raw) != entry.DecodedSHA || compressed.Len() != 0 {
			t.Fatal("incomplete engineering artifact", path, err)
		}
		decoded[filepath.Base(path)] = raw
	}
	for name, generate := range map[string]func() ([]byte, error){"content-v4.json.gz": MaterializePermissionContent, "release-assembly-report-v4.json.gz": AssemblePermissionRelease} {
		fresh, err := generate()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fresh, decoded[name]) {
			t.Fatal("actual dynamic content drift", name)
		}
	}
	var report struct {
		CatalogDigest any `json:"catalog_digest"`
		Bytes         int `json:"assembled_bytes"`
		Limit         int `json:"artifact_limit"`
	}
	if json.Unmarshal(decoded["release-assembly-report-v4.json.gz"], &report) != nil || report.CatalogDigest != nil || report.Bytes <= report.Limit || len(supportedPermissionReleases) != 0 {
		t.Fatal("engineering report promoted to supported release")
	}
}
