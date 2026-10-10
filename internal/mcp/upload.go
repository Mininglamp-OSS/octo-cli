package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
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
	absRoot, absErr := filepath.Abs(root)
	if absErr != nil || filepath.Dir(absRoot) == absRoot {
		return "", "", errors.New("upload root must not be a filesystem root")
	}
	if pol.httpMode {
		if err := validateHTTPUploadRoot(root); err != nil {
			return "", "", err
		}
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
	parent := filepath.Dir(configured)
	// Resolve only trusted ancestors, never the root or the supplied file.
	// A missing ancestor may still be planned lexically; execution must open it.
	resolvedParent, resolveErr := filepath.EvalSymlinks(parent)
	if resolveErr == nil {
		parent = resolvedParent
	} else if !pol.dryRun {
		return "", "", errors.New("configured upload root is not accessible")
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

// HTTP may read only from a concrete operator-owned staging directory. A
// filesystem root or host system tree would make the bearer an arbitrary-file
// upload capability, equivalent to the stdio-only unconfined opt-in.
func validateHTTPUploadRoot(root string) error { //nolint:gocyclo // filesystem admission checks are intentionally explicit and fail closed
	if strings.TrimSpace(root) == "" {
		return nil // uploads remain disabled
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return errors.New("configured HTTP upload root is invalid")
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("configured HTTP upload root must be an existing non-symlink directory")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return errors.New("configured HTTP upload root is not accessible")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.New("configured HTTP upload root cannot be checked against private directories")
	}
	if filepath.Dir(resolved) == resolved {
		return errors.New("HTTP upload root must not be a filesystem root")
	}
	for _, system := range []string{"/proc", "/sys", "/dev", "/etc", "/private/etc", "/run", "/var/run", "/private/var/run", "/System", "/Library", "/usr", "/opt", "/boot"} {
		if resolved == system || strings.HasPrefix(resolved, system+string(filepath.Separator)) {
			return errors.New("HTTP upload root must not be a host system directory")
		}
	}
	for _, broad := range []string{"/home", "/root", "/Users", "/tmp", "/private/tmp", "/var", "/private/var"} {
		if resolved == broad {
			return errors.New("HTTP upload root must be a dedicated staging directory")
		}
		// Home collections and the user's home directory are not staging roots.
		if broad == "/home" || broad == "/Users" {
			if filepath.Dir(resolved) == broad {
				return errors.New("HTTP upload root must not be a user home directory")
			}
		}
	}
	for i, sensitive := range []string{home, filepath.Join(home, ".octo-cli"), os.Getenv("OCTO_CONFIG_DIR")} {
		if sensitive == "" {
			continue
		}
		path, pathErr := filepath.Abs(sensitive)
		if pathErr != nil {
			return errors.New("configured HTTP upload root cannot be checked against private directories")
		}
		if canonical, canonicalErr := filepath.EvalSymlinks(path); canonicalErr == nil {
			path = canonical
		}
		if resolved == path || strings.HasPrefix(path, resolved+string(filepath.Separator)) ||
			(i > 0 && strings.HasPrefix(resolved, path+string(filepath.Separator))) {
			return errors.New("HTTP upload root must not contain the home or credential directory")
		}
	}
	marker, markerErr := os.Lstat(filepath.Join(resolved, ".octo-mcp-upload-root"))
	if markerErr != nil || !marker.Mode().IsRegular() {
		return errors.New("HTTP upload root needs a regular .octo-mcp-upload-root marker file")
	}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if system := strings.TrimSpace(os.Getenv(key)); system != "" {
			system = filepath.Clean(system)
			if strings.EqualFold(resolved, system) || strings.HasPrefix(strings.ToLower(resolved), strings.ToLower(system)+string(filepath.Separator)) {
				return errors.New("HTTP upload root must not be a host system directory")
			}
		}
	}
	return nil
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
		return nil, errors.New("file_path is not accessible")
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
