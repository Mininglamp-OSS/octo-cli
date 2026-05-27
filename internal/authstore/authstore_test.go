package authstore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// newTestStore points the store at an isolated temp dir via OCTO_CONFIG_DIR.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv(EnvConfigDir, t.TempDir())
	s, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestStore_RoundTrip(t *testing.T) {
	s := newTestStore(t)

	meta := ProfileMeta{APIBaseURL: "https://api.example.com", BotKind: "app_bot", RobotID: "cli_demo"}
	if err := s.SaveProfile("prod", &meta, "app_secret_token"); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	got, err := s.GetToken("prod")
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if got != "app_secret_token" {
		t.Errorf("token = %q", got)
	}

	profiles, err := s.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if profiles["prod"].RobotID != "cli_demo" {
		t.Errorf("robot_id = %q", profiles["prod"].RobotID)
	}
}

func TestStore_CiphertextHidesToken(t *testing.T) {
	s := newTestStore(t)
	const secret = "app_super_secret_value"
	if err := s.SaveProfile("prod", &ProfileMeta{RobotID: "cli_x"}, secret); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	blob, err := os.ReadFile(s.credPath())
	if err != nil {
		t.Fatalf("read enc: %v", err)
	}
	if bytes.Contains(blob, []byte(secret)) {
		t.Error("credentials.enc contains the plaintext token")
	}

	// config.json must never contain the token either.
	cfg, err := os.ReadFile(s.configPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if bytes.Contains(cfg, []byte(secret)) {
		t.Error("config.json contains the plaintext token")
	}
}

func TestStore_FilePermissions(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProfile("p", &ProfileMeta{RobotID: "cli_x"}, "app_t"); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	for _, p := range []string{s.credPath(), s.saltPath()} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := info.Mode().Perm(); perm != secPerm {
			t.Errorf("%s perm = %o, want %o", filepath.Base(p), perm, secPerm)
		}
	}
}

func TestStore_Remove(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProfile("a", &ProfileMeta{RobotID: "cli_a"}, "app_a"); err != nil {
		t.Fatalf("SaveProfile a: %v", err)
	}
	if err := s.SaveProfile("b", &ProfileMeta{RobotID: "cli_b"}, "app_b"); err != nil {
		t.Fatalf("SaveProfile b: %v", err)
	}
	if err := s.RemoveProfile("a"); err != nil {
		t.Fatalf("RemoveProfile: %v", err)
	}
	if _, err := s.GetToken("a"); err == nil {
		t.Error("token for removed profile still present")
	}
	if _, err := s.GetToken("b"); err != nil {
		t.Errorf("token for b lost after removing a: %v", err)
	}
	n, _ := s.Count()
	if n != 1 {
		t.Errorf("Count = %d, want 1", n)
	}
}

func TestStore_RemoveLastClearsEncFile(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProfile("only", &ProfileMeta{RobotID: "cli_o"}, "app_o"); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if err := s.RemoveProfile("only"); err != nil {
		t.Fatalf("RemoveProfile: %v", err)
	}
	if _, err := os.Stat(s.credPath()); !os.IsNotExist(err) {
		t.Errorf("credentials.enc should be removed when empty, stat err = %v", err)
	}
}

func TestStore_CorruptEncErrors(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProfile("p", &ProfileMeta{RobotID: "cli_x"}, "app_t"); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	// Flip bytes to break GCM authentication.
	if err := os.WriteFile(s.credPath(), []byte("not-a-valid-ciphertext-blob!!"), secPerm); err != nil {
		t.Fatalf("corrupt write: %v", err)
	}
	if _, err := s.GetToken("p"); err == nil {
		t.Error("expected error decrypting corrupt credentials.enc")
	}
}

// TestStore_OrphanedTokenIsHarmless simulates the post-crash residue of the
// write ordering (token written, metadata not yet): a token in credentials.enc
// with no config.json entry must be invisible and must not break resolution.
func TestStore_OrphanedTokenIsHarmless(t *testing.T) {
	s := newTestStore(t)
	if err := s.saveTokens(map[string]string{"ghost": "app_ghost"}); err != nil {
		t.Fatalf("seed orphan token: %v", err)
	}

	profiles, err := s.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("orphan token leaked into the catalog: %v", profiles)
	}
	if _, _, status, _ := s.ActiveProfile("", ""); status != StatusNone {
		t.Errorf("status = %v, want StatusNone (orphan must not count as a profile)", status)
	}

	// A real save still works alongside the orphan.
	if err := s.SaveProfile("real", &ProfileMeta{RobotID: "cli_real"}, "app_real"); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if tok, err := s.GetToken("real"); err != nil || tok != "app_real" {
		t.Errorf("GetToken(real) = %q, %v", tok, err)
	}
}

func TestActiveProfile(t *testing.T) {
	type want struct {
		name   string
		status Status
	}
	cases := []struct {
		desc     string
		profiles map[string]ProfileMeta
		name     string
		botID    string
		want     want
	}{
		{
			desc:     "no profiles -> none",
			profiles: map[string]ProfileMeta{},
			want:     want{"", StatusNone},
		},
		{
			desc:     "single profile, no selector -> found",
			profiles: map[string]ProfileMeta{"only": {RobotID: "cli_o"}},
			want:     want{"only", StatusFound},
		},
		{
			desc:     "two profiles, no selector -> ambiguous",
			profiles: map[string]ProfileMeta{"a": {RobotID: "cli_a"}, "b": {RobotID: "cli_b"}},
			want:     want{"", StatusAmbiguous},
		},
		{
			desc:     "select by name -> found",
			profiles: map[string]ProfileMeta{"a": {RobotID: "cli_a"}, "b": {RobotID: "cli_b"}},
			name:     "b",
			want:     want{"b", StatusFound},
		},
		{
			desc:     "select by name missing -> missing",
			profiles: map[string]ProfileMeta{"a": {RobotID: "cli_a"}},
			name:     "nope",
			want:     want{"", StatusMissing},
		},
		{
			desc:     "select by bot-id -> found",
			profiles: map[string]ProfileMeta{"a": {RobotID: "cli_a"}, "b": {RobotID: "cli_b"}},
			botID:    "cli_b",
			want:     want{"b", StatusFound},
		},
		{
			desc:     "select by bot-id no match -> missing",
			profiles: map[string]ProfileMeta{"a": {RobotID: "cli_a"}},
			botID:    "cli_zzz",
			want:     want{"", StatusMissing},
		},
		{
			desc:     "both consistent -> found",
			profiles: map[string]ProfileMeta{"a": {RobotID: "cli_a"}},
			name:     "a",
			botID:    "cli_a",
			want:     want{"a", StatusFound},
		},
		{
			desc:     "both inconsistent -> missing",
			profiles: map[string]ProfileMeta{"a": {RobotID: "cli_a"}},
			name:     "a",
			botID:    "cli_b",
			want:     want{"", StatusMissing},
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			s := newTestStore(t)
			for n, m := range c.profiles {
				if err := s.SaveProfile(n, &m, "app_"+n); err != nil {
					t.Fatalf("seed %s: %v", n, err)
				}
			}
			name, _, status, err := s.ActiveProfile(c.name, c.botID)
			if err != nil {
				t.Fatalf("ActiveProfile: %v", err)
			}
			if status != c.want.status {
				t.Errorf("status = %v, want %v", status, c.want.status)
			}
			if name != c.want.name {
				t.Errorf("name = %q, want %q", name, c.want.name)
			}
		})
	}
}
