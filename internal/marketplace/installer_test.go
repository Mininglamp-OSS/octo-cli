package marketplace

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveEntry struct {
	name string
	body string
	mode os.FileMode
}

func makeArchive(t *testing.T, entries ...archiveEntry) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		mode := entry.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestInstallSuccessReplacesOnlyTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other", "keep"), []byte("yes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "demo", "old"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, digest := makeArchive(t,
		archiveEntry{name: "SKILL.md", body: "# demo"},
		archiveEntry{name: "references/readme.md", body: "reference"},
	)
	result, err := Install(root, "demo", archive, digest)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.InstalledTo != filepath.Join(root, "demo") || len(result.Files) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", "old")); !os.IsNotExist(err) {
		t.Fatalf("old file was not replaced: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "other", "keep")); err != nil {
		t.Fatalf("unrelated skill changed: %v", err)
	}
}

func TestInstallStripsSinglePackagingDirectory(t *testing.T) {
	archive, digest := makeArchive(t,
		archiveEntry{name: "DeepMiner-skills/SKILL.md", body: "# demo"},
		archiveEntry{name: "DeepMiner-skills/references/readme.md", body: "reference"},
	)
	root := t.TempDir()
	result, err := Install(root, "DM-skills", archive, digest)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.InstalledTo, "SKILL.md")); err != nil {
		t.Fatalf("root SKILL.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.InstalledTo, "DeepMiner-skills")); !os.IsNotExist(err) {
		t.Fatalf("packaging directory was not stripped: %v", err)
	}
}

func TestInstallRejectsUnsafeArchive(t *testing.T) {
	tests := []struct {
		name    string
		entries []archiveEntry
		want    string
	}{
		{"path traversal", []archiveEntry{{name: "../escape", body: "x"}, {name: "SKILL.md", body: "x"}}, "unsafe path"},
		{"symlink", []archiveEntry{{name: "SKILL.md", body: "x"}, {name: "link", body: "SKILL.md", mode: os.ModeSymlink | 0o777}}, "unsupported"},
		{"missing skill", []archiveEntry{{name: "README.md", body: "x"}}, "missing SKILL.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive, digest := makeArchive(t, tt.entries...)
			_, err := Install(t.TempDir(), "demo", archive, digest)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	archive, _ := makeArchive(t, archiveEntry{name: "SKILL.md", body: "x"})
	if _, err := Install(t.TempDir(), "demo", archive, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("error = %v", err)
	}
}

func TestInstallPreservesBackupWhenActivationAndRollbackFail(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "old"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, digest := makeArchive(t, archiveEntry{name: "SKILL.md", body: "new"})

	renameCalls := 0
	rename := func(oldPath, newPath string) error {
		renameCalls++
		switch renameCalls {
		case 1:
			return os.Rename(oldPath, newPath)
		case 2:
			return errors.New("activation failure")
		default:
			return errors.New("rollback failure")
		}
	}

	_, err := installWithRename(root, "demo", archive, digest, rename)
	if err == nil || !strings.Contains(err.Error(), "previous Skill preserved at") {
		t.Fatalf("error = %v", err)
	}
	backups, globErr := filepath.Glob(filepath.Join(root, ".demo-backup-*"))
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, err = %v", backups, globErr)
	}
	old, readErr := os.ReadFile(filepath.Join(backups[0], "old"))
	if readErr != nil || string(old) != "old" {
		t.Fatalf("preserved old Skill = %q, err = %v", old, readErr)
	}
}
