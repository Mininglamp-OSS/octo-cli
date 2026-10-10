package mcp

import (
	"errors"
	"fmt"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"os"
	"path/filepath"
	"strings"
)

// uploadLocation performs lexical policy checks only. Path validation is not
// an authorization to reopen the name: openUpload enforces the boundary on
// descriptors, and executeOperation lends that descriptor to the engine.
func uploadLocation(p string, pol execPolicy) (root, relative string, err error) { //nolint:gocyclo // root aliases and file containment have distinct fail-closed checks
	root = strings.TrimSpace(pol.uploadRoot)
	if root == "" {
		if pol.allowLocalUpload && !pol.httpMode {
			return "", p, nil
		}
		return "", "", errors.New("local file_path upload is disabled; set OCTO_MCP_UPLOAD_ROOT (or use --allow-local-upload on trusted stdio only)")
	}
	if uploadTraversal(p) {
		return "", "", errors.New("file_path traversal is not allowed")
	}
	configured, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	// System aliases in trusted ancestors (e.g. macOS /var) are canonicalized,
	// but the configured root itself must be opened without following a link.
	// Otherwise replacing the root with a link would redefine confinement.
	parent, err := filepath.EvalSymlinks(filepath.Dir(configured))
	if err != nil {
		return "", "", fmt.Errorf("configured upload root is not accessible: %w", err)
	}
	root = filepath.Join(parent, filepath.Base(configured))
	if info, statErr := os.Lstat(root); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("configured upload root must not be a symlink")
	}
	relative = p
	if filepath.IsAbs(p) {
		relative, err = filepath.Rel(configured, p)
		if err != nil || escapesUploadRoot(relative) {
			relative, err = filepath.Rel(root, p)
		}
	}
	if err != nil || escapesUploadRoot(relative) {
		return "", "", errors.New("file_path escapes the configured upload root")
	}
	relative = filepath.Clean(relative)
	if relative == "." || relative == "" {
		return "", "", errors.New("file_path must name a regular file")
	}
	return root, relative, nil
}

func uploadTraversal(p string) bool {
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r < 128 && os.IsPathSeparator(uint8(r)) }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func escapesUploadRoot(p string) bool {
	return !filepath.IsLocal(p)
}

// resolveUploadPath supplies the display/filename flag. It never grants access;
// the actual read is authorized separately by openUpload, exactly once.
func resolveUploadPath(p string, pol execPolicy) (string, error) {
	root, rel, err := uploadLocation(p, pol)
	if err != nil {
		return "", err
	}
	if root == "" {
		return rel, nil
	}
	return filepath.Join(root, rel), nil
}

const maxMCPUploadBytes = cmdutil.MaxMCPUploadBytes

func openUpload(p string, pol execPolicy) (*os.File, error) {
	root, relative, err := uploadLocation(p, pol)
	if err != nil {
		return nil, err
	}
	var file *os.File
	if root == "" {
		file, err = openLocalUpload(relative)
	} else {
		var dir *os.File
		dir, err = openUploadRoot(root)
		if err == nil {
			defer dir.Close() //nolint:errcheck // directory descriptor cleanup
			file, err = walkUpload(dir, relative, openUploadComponent)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("file_path is not accessible: %w", err)
	}
	info, err := file.Stat() // fstat the descriptor, not a pathname that can change
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close() //nolint:errcheck // reject and release the descriptor
		return nil, errors.New("file_path must name a regular file")
	}
	if root != "" {
		if err := rejectUploadHardlink(file); err != nil {
			_ = file.Close() //nolint:errcheck // release rejected descriptor
			return nil, err
		}
	}

	if info.Size() > maxMCPUploadBytes {
		_ = file.Close() //nolint:errcheck // release the rejected upload descriptor
		return nil, errors.New("file_path exceeds the 32 MiB MCP upload limit")
	}

	return file, nil
}

type uploadComponentOpener func(parent *os.File, name string, directory bool) (*os.File, error)

// walkUpload resolves one component at a time relative to held directory
// handles. No component may be a symlink/reparse point. Renaming a directory
// cannot redirect the next lookup; each parent remains the opened object.
func walkUpload(root *os.File, relative string, open uploadComponentOpener) (*os.File, error) {
	parts := strings.Split(relative, string(os.PathSeparator))
	parent := root
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			if parent != root {
				_ = parent.Close() //nolint:errcheck // release walked directory on invalid input
			}
			return nil, errors.New("invalid upload path component")
		}
		next, err := open(parent, part, i < len(parts)-1)
		if parent != root {
			_ = parent.Close() //nolint:errcheck // directory descriptor cleanup
		}
		if err != nil {
			return nil, err
		}
		parent = next
	}
	return parent, nil
}
