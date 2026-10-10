package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func markUploadRoot(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".octo-mcp-upload-root"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}
