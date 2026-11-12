package permission

import (
	"context"
	"io"
	"os"
	"strings"
)

// ReadCatalogPackageDirectory pins an isolated directory and never follows an
// unlisted path. Both inventories and file identities are checked around reads;
// the returned snapshot contains detached verified bytes, never filesystem views.
func ReadCatalogPackageDirectory(ctx context.Context, dir string, pin CatalogPin, baseline CatalogBaseline, slots CatalogSlotValues) (*VerifiedCatalog, error) { //nolint:gocyclo // pin inventory and file identities before whole-snapshot validation
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, ErrUnavailable
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer closeCatalogFile(root)
	read := func(path string, maximum int) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		before, err := root.Lstat(path)
		if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o222 != 0 || !catalogSingleLink(before) || before.Size() < 0 || before.Size() > int64(maximum) {
			return nil, ErrUnavailable
		}
		f, err := root.Open(path)
		if err != nil {
			return nil, ErrUnavailable
		}
		defer closeCatalogFile(f)
		opened, err := f.Stat()
		if err != nil || !os.SameFile(before, opened) {
			return nil, ErrUnavailable
		}
		raw, err := io.ReadAll(io.LimitReader(f, int64(maximum)+1))
		after, statErr := root.Lstat(path)
		closed, fdErr := f.Stat()
		if err != nil || statErr != nil || fdErr != nil || !os.SameFile(before, after) || !os.SameFile(before, closed) || !catalogSingleLink(after) || after.Mode() != before.Mode() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || len(raw) > maximum {
			return nil, ErrUnavailable
		}
		return raw, ctx.Err()
	}
	manifest, err := read("manifest.json", 65536)
	if err != nil {
		return nil, err
	}
	if catalogHash(manifest) != pin.Digest || ValidateCatalogDefinition("Manifest", manifest) != nil {
		return nil, ErrUnavailable
	}
	value, _, err := catalogJSONValue(manifest, true)
	if err != nil {
		return nil, err
	}
	expected := map[string]int{"manifest.json": 65536}
	dirs := map[string]bool{".": true}
	stored := len(manifest)
	for _, item := range catalogArray(object(value)["files"]) {
		file := object(item)
		path := text(file["path"])
		size := catalogNumber(file["bytes"])
		if _, exists := expected[path]; exists || size > 8388608-stored {
			return nil, ErrUnavailable
		}
		stored += size
		maximum := 262144
		if file["kind"] == "blob" {
			maximum = 1048576
		}
		expected[path] = maximum
		parts := strings.Split(path, "/")
		prefix := ""
		for _, part := range parts[:len(parts)-1] {
			if prefix != "" {
				prefix += "/"
			}
			prefix += part
			dirs[prefix] = true
		}
	}
	inventory := func() error {
		seen := map[string]bool{}
		var walk func(string) error
		walk = func(path string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			f, err := root.Open(path)
			if err != nil {
				return ErrUnavailable
			}
			entries, readErr := f.ReadDir(-1)
			closeCatalogFile(f)
			if readErr != nil {
				return ErrUnavailable
			}
			for _, entry := range entries {
				child := entry.Name()
				if path != "." {
					child = path + "/" + child
				}
				info, err := root.Lstat(child)
				if err != nil || info.Mode()&os.ModeSymlink != 0 {
					return ErrUnavailable
				}
				if info.IsDir() {
					if !dirs[child] {
						return ErrUnavailable
					}
					if err := walk(child); err != nil {
						return err
					}
				} else {
					if _, ok := expected[child]; !ok || !info.Mode().IsRegular() || !catalogSingleLink(info) {
						return ErrUnavailable
					}
					seen[child] = true
				}
			}
			return nil
		}
		if err := walk("."); err != nil {
			return err
		}
		if len(seen) != len(expected) {
			return ErrUnavailable
		}
		return nil
	}
	if err := inventory(); err != nil {
		return nil, err
	}
	files := map[string][]byte{"manifest.json": manifest}
	for path, maximum := range expected {
		if path == "manifest.json" {
			continue
		}
		raw, err := read(path, maximum)
		if err != nil {
			return nil, err
		}
		files[path] = raw
	}
	if err := inventory(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return VerifyCatalogPackage(ctx, files, pin, baseline, slots)
}

func closeCatalogFile(f io.Closer) { _ = f.Close() } //nolint:errcheck // immutable snapshot validation cannot gain authority from cleanup
