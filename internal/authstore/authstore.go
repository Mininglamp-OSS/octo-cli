// Package authstore persists bot credentials on disk: non-secret profile
// metadata in plaintext config.json and the tokens themselves in an AES-256-GCM
// encrypted credentials.enc. Everything lives in a single per-user directory
// (~/.octo-cli by default, override with OCTO_CONFIG_DIR).
//
// The trust boundary is the OS user account: the encryption key is derived from
// machine-bound material, so the encrypted file resists off-machine leakage
// (accidental commit, backup, cloud sync) but not a same-user process that can
// run octo. Isolating mutually-distrusting bots requires separate OS users or
// separate OCTO_CONFIG_DIR values — the CLI does not pretend otherwise.
package authstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// EnvConfigDir overrides the default config directory when set.
const EnvConfigDir = "OCTO_CONFIG_DIR"

const (
	configFile = "config.json"
	credFile   = "credentials.enc"
	saltFile   = "cred.salt"

	dirPerm  os.FileMode = 0o700
	metaPerm os.FileMode = 0o644
	secPerm  os.FileMode = 0o600
)

// Status is the outcome of resolving an active profile from selectors.
type Status int

const (
	// StatusFound: exactly one profile resolved (by selector or as the sole entry).
	StatusFound Status = iota
	// StatusNone: zero profiles configured — caller should fall back to env.
	StatusNone
	// StatusAmbiguous: >=2 profiles and no selector given — caller must error.
	StatusAmbiguous
	// StatusMissing: a selector was given but matched nothing / was inconsistent.
	StatusMissing
)

// ProfileMeta is the non-secret metadata stored per profile (never the token).
type ProfileMeta struct {
	APIBaseURL string `json:"api_base_url,omitempty"`
	SpaceID    string `json:"space_id,omitempty"`
	BotKind    string `json:"bot_kind,omitempty"`
	RobotID    string `json:"robot_id,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

// configFileShape is the on-disk config.json layout.
type configFileShape struct {
	Profiles map[string]ProfileMeta `json:"profiles"`
}

// Store reads and writes the credential directory.
type Store struct {
	dir string
}

// New resolves the config directory and returns a Store. The directory is not
// created until a write occurs, so read-only callers on a fresh machine see an
// empty store rather than an error.
func New() (*Store, error) {
	dir, err := resolveDir()
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Dir returns the resolved config directory.
func (s *Store) Dir() string { return s.dir }

func resolveDir() (string, error) {
	if d := os.Getenv(EnvConfigDir); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".octo-cli"), nil
}

func (s *Store) configPath() string { return filepath.Join(s.dir, configFile) }
func (s *Store) credPath() string   { return filepath.Join(s.dir, credFile) }
func (s *Store) saltPath() string   { return filepath.Join(s.dir, saltFile) }

// LoadProfiles returns the profile metadata map. A missing config.json yields an
// empty (non-nil) map.
func (s *Store) LoadProfiles() (map[string]ProfileMeta, error) {
	data, err := os.ReadFile(s.configPath())
	if os.IsNotExist(err) {
		return map[string]ProfileMeta{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", configFile, err)
	}
	var cf configFileShape
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configFile, err)
	}
	if cf.Profiles == nil {
		cf.Profiles = map[string]ProfileMeta{}
	}
	return cf.Profiles, nil
}

// Count returns the number of configured profiles.
func (s *Store) Count() (int, error) {
	p, err := s.LoadProfiles()
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *Store) saveProfiles(profiles map[string]ProfileMeta) error {
	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", s.dir, err)
	}
	data, err := json.MarshalIndent(configFileShape{Profiles: profiles}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", configFile, err)
	}
	return atomicWrite(s.configPath(), append(data, '\n'), metaPerm)
}

// SaveProfile stores (or replaces) a profile's metadata and token.
//
// The two files can't be written atomically together, so order them to keep the
// invariant "a profile listed in config.json has a token in credentials.enc":
// write the token first, then the metadata. A crash in between leaves an
// orphaned token (invisible, since nothing lists it) rather than a listed
// profile with no token.
func (s *Store) SaveProfile(name string, meta ProfileMeta, token string) error {
	profiles, err := s.LoadProfiles()
	if err != nil {
		return err
	}
	tokens, err := s.loadTokens()
	if err != nil {
		return err
	}
	profiles[name] = meta
	tokens[name] = token
	if err := s.saveTokens(tokens); err != nil {
		return err
	}
	return s.saveProfiles(profiles)
}

// RemoveProfile deletes a profile's metadata and token. Removing an absent
// profile is a no-op.
//
// Mirror SaveProfile's ordering to preserve the same invariant: drop the
// metadata first, then the token. A crash in between leaves an orphaned token
// (harmless) rather than a listed profile whose token is already gone.
func (s *Store) RemoveProfile(name string) error {
	profiles, err := s.LoadProfiles()
	if err != nil {
		return err
	}
	tokens, err := s.loadTokens()
	if err != nil {
		return err
	}
	delete(profiles, name)
	delete(tokens, name)
	if err := s.saveProfiles(profiles); err != nil {
		return err
	}
	return s.saveTokens(tokens)
}

// GetToken returns the token for a profile, or an error if absent.
func (s *Store) GetToken(name string) (string, error) {
	tokens, err := s.loadTokens()
	if err != nil {
		return "", err
	}
	tok, ok := tokens[name]
	if !ok {
		return "", fmt.Errorf("no stored token for profile %q", name)
	}
	return tok, nil
}

// ActiveProfile resolves which profile to use from the given selectors:
//   - both name and botID: select by name, require its robot_id == botID
//   - botID only: select the profile whose robot_id == botID
//   - name only: select by name
//   - neither: sole profile if exactly one, else ambiguous/none
//
// The returned status tells the caller how to react (found / none / ambiguous /
// missing). resolvedName and meta are only meaningful when status is found.
func (s *Store) ActiveProfile(name, botID string) (string, ProfileMeta, Status, error) {
	profiles, err := s.LoadProfiles()
	if err != nil {
		return "", ProfileMeta{}, StatusMissing, err
	}

	switch {
	case name != "" && botID != "":
		meta, ok := profiles[name]
		if !ok || meta.RobotID != botID {
			return "", ProfileMeta{}, StatusMissing, nil
		}
		return name, meta, StatusFound, nil

	case botID != "":
		if n, meta, ok := findByRobotID(profiles, botID); ok {
			return n, meta, StatusFound, nil
		}
		return "", ProfileMeta{}, StatusMissing, nil

	case name != "":
		meta, ok := profiles[name]
		if !ok {
			return "", ProfileMeta{}, StatusMissing, nil
		}
		return name, meta, StatusFound, nil

	default:
		switch len(profiles) {
		case 0:
			return "", ProfileMeta{}, StatusNone, nil
		case 1:
			n := soleKey(profiles)
			return n, profiles[n], StatusFound, nil
		default:
			return "", ProfileMeta{}, StatusAmbiguous, nil
		}
	}
}

func findByRobotID(profiles map[string]ProfileMeta, robotID string) (string, ProfileMeta, bool) {
	// Deterministic order so a duplicated robot_id resolves consistently.
	names := sortedKeys(profiles)
	for _, n := range names {
		if profiles[n].RobotID == robotID {
			return n, profiles[n], true
		}
	}
	return "", ProfileMeta{}, false
}

func soleKey(profiles map[string]ProfileMeta) string {
	for k := range profiles {
		return k
	}
	return ""
}

func sortedKeys(profiles map[string]ProfileMeta) []string {
	names := make([]string, 0, len(profiles))
	for k := range profiles {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// atomicWrite writes data to a temp file in the same directory then renames it
// into place, so a crash never leaves a half-written file.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}
	return nil
}
