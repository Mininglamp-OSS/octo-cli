package skillinstall

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxFiles     = 1024
	maxTotalSize = int64(32 << 20)
	maxFileSize  = int64(16 << 20)
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type Result struct {
	InstalledTo string
	Files       []string
}

func Install(root, name string, archive []byte, expectedSHA string) (result Result, err error) {
	if !namePattern.MatchString(name) {
		return Result{}, fmt.Errorf("invalid skill name %q", name)
	}
	sum := sha256.Sum256(archive)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, expectedSHA) {
		return Result{}, fmt.Errorf("skill archive checksum mismatch")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve install root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("create install root: %w", err)
	}
	tmp, err := os.MkdirTemp(absRoot, "."+name+"-install-")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	files, err := extract(tmp, archive)
	if err != nil {
		return Result{}, err
	}
	if info, statErr := os.Stat(filepath.Join(tmp, "SKILL.md")); statErr != nil || !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("skill archive is missing SKILL.md")
	}

	target := filepath.Join(absRoot, name)
	backup, err := os.MkdirTemp(absRoot, "."+name+"-backup-")
	if err != nil {
		return Result{}, fmt.Errorf("reserve backup path: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return Result{}, fmt.Errorf("prepare backup path: %w", err)
	}
	defer func() { _ = os.RemoveAll(backup) }()
	hadTarget := false
	if _, statErr := os.Stat(target); statErr == nil {
		if err := os.Rename(target, backup); err != nil {
			return Result{}, fmt.Errorf("backup existing skill: %w", err)
		}
		hadTarget = true
	} else if !os.IsNotExist(statErr) {
		return Result{}, fmt.Errorf("inspect existing skill: %w", statErr)
	}
	if err := os.Rename(tmp, target); err != nil {
		if hadTarget {
			_ = os.Rename(backup, target)
		}
		return Result{}, fmt.Errorf("activate installed skill: %w", err)
	}
	if hadTarget {
		_ = os.RemoveAll(backup)
	}
	return Result{InstalledTo: target, Files: files}, nil
}

func extract(dst string, archive []byte) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open skill archive: %w", err)
	}
	files := make([]string, 0)
	var total int64
	for _, entry := range zr.File {
		clean := filepath.Clean(filepath.FromSlash(entry.Name))
		if entry.Name == "" || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("unsafe path in skill archive: %q", entry.Name)
		}
		path := filepath.Join(dst, clean)
		rel, relErr := filepath.Rel(dst, path)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("path escapes install directory: %q", entry.Name)
		}
		info := entry.FileInfo()
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("unsupported archive entry type for %q", entry.Name)
		}
		if info.IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return nil, fmt.Errorf("create archive directory: %w", err)
			}
			continue
		}
		if len(files) >= maxFiles {
			return nil, fmt.Errorf("skill archive exceeds file count limit")
		}
		size := int64(entry.UncompressedSize64)
		if size < 0 || size > maxFileSize || total+size > maxTotalSize {
			return nil, fmt.Errorf("skill archive exceeds size limit")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create archive parent: %w", err)
		}
		rc, openErr := entry.Open()
		if openErr != nil {
			return nil, fmt.Errorf("open archive file: %w", openErr)
		}
		f, createErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		if createErr != nil {
			_ = rc.Close()
			return nil, fmt.Errorf("create archive file: %w", createErr)
		}
		_, copyErr := io.CopyN(f, rc, size)
		archiveCloseErr := rc.Close()
		fileCloseErr := f.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("extract archive file: %w", copyErr)
		}
		if archiveCloseErr != nil || fileCloseErr != nil {
			return nil, fmt.Errorf("close archive file")
		}
		total += size
		files = append(files, filepath.ToSlash(clean))
	}
	sort.Strings(files)
	return files, nil
}
