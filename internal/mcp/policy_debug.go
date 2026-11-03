package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
)

// DebugPolicy is an operator-selected immutable startup snapshot. Its private
// map, origin and hash are never sent to callers or the request store.
type DebugPolicy struct {
	origin, stage string
	bots          map[string]string
	hash          [32]byte
}

// LoadDebugPolicyFile validates a private operator file and freezes its policy.
func LoadDebugPolicyFile(path string) (*DebugPolicy, error) { //nolint:gocyclo // fail closed at every file-safety and closed-schema boundary
	unsafe := errors.New("DEBUG_POLICY_FILE_UNSAFE")
	if !filepath.IsAbs(path) {
		return nil, unsafe
	}
	for _, p := range strings.Split(path, "/") {
		if p == ".." {
			return nil, unsafe
		}
	}
	dir, err := secureDirectory(filepath.Dir(path), false)
	if err != nil {
		return nil, unsafe
	}
	defer closePrivateFile(dir)
	f, err := openDebugAt(dir, filepath.Base(path))
	if err != nil {
		return nil, unsafe
	}
	defer closePrivateFile(f)
	before, err := f.Stat()
	if err != nil {
		return nil, unsafe
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || !sameDebugFile(dir, f, filepath.Base(path), before) {
		return nil, unsafe
	}
	invalid := errors.New("DEBUG_POLICY_FILE_INVALID")
	if len(raw) > 65536 {
		return nil, invalid
	}
	m, err := strictObject(raw, "version", "api_base_url", "stage_default", "bot_policies")
	if err != nil || string(m["version"]) != "1" {
		return nil, invalid
	}
	var origin, stage string
	if json.Unmarshal(m["api_base_url"], &origin) != nil || !validID(origin, 2048) || json.Unmarshal(m["stage_default"], &stage) != nil || !validMode(stage) {
		return nil, invalid
	}
	norm, err := config.NormalizeAPIBaseURL(origin)
	if err != nil || !strings.HasPrefix(norm, "https://") || strings.TrimRight(origin, "/") != origin {
		return nil, errors.New("DEBUG_POLICY_ORIGIN_INVALID")
	}
	bots, err := strictObject(m["bot_policies"])
	if err != nil || len(bots) > 256 {
		return nil, invalid
	}
	d := &DebugPolicy{origin: norm, stage: stage, bots: make(map[string]string), hash: sha256.Sum256(raw)}
	for uid, value := range bots {
		if !validID(uid, 40) {
			return nil, invalid
		}
		entry, e := strictObject(value, "context_mode")
		if e != nil {
			return nil, invalid
		}
		var mode string
		if json.Unmarshal(entry["context_mode"], &mode) != nil || !validMode(mode) {
			return nil, invalid
		}
		d.bots[uid] = mode
	}
	return d, nil
}

func (d *DebugPolicy) resolve(uid, origin string) (string, error) {
	if d == nil || d.origin != origin {
		return "", policyFailure()
	}
	if mode, ok := d.bots[uid]; ok {
		return mode, nil
	}
	return d.stage, nil
}
