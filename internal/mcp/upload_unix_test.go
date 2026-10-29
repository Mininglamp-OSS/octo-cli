//go:build linux || darwin

package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenUpload_RejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for _, policy := range []execPolicy{{uploadRoot: root}, {uploadRoot: root, httpMode: true}, {allowLocalUpload: true}} {
		file, err := openUpload(path, policy)
		if err == nil {
			file.Close()
			t.Fatal("FIFO must be rejected by descriptor type")
		}
	}
	if time.Since(start) > time.Second {
		t.Fatal("FIFO open blocked before descriptor validation")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
