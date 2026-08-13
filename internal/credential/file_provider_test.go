package credential

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// fileTestStore returns a Store backed by an isolated temp dir, seeded with the
// given profile→robotID pairs.
func fileTestStore(t *testing.T, profiles map[string]string) *authstore.Store {
	t.Helper()
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	s, err := authstore.New()
	if err != nil {
		t.Fatalf("authstore.New: %v", err)
	}
	for name, robotID := range profiles {
		meta := authstore.ProfileMeta{RobotID: robotID, BotKind: "app_bot"}
		if err := s.SaveProfile(name, &meta, "app_"+name); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	return s
}

func exitType(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("error is not an ExitError: %v", err)
	}
	return ee.Type
}

func TestFileProvider_SingleProfileNoSelector(t *testing.T) {
	s := fileTestStore(t, map[string]string{"only": "cli_only"})
	cred, err := NewFileProvider(s, "", "").Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred == nil || cred.Profile != "only" || cred.RobotID != "cli_only" {
		t.Fatalf("cred = %+v", cred)
	}
	if cred.Source != "profile:only" {
		t.Errorf("source = %q", cred.Source)
	}
}

func TestFileProvider_SelectByName(t *testing.T) {
	s := fileTestStore(t, map[string]string{"a": "cli_a", "b": "cli_b"})
	cred, err := NewFileProvider(s, "b", "").Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Profile != "b" || cred.Token != "app_b" {
		t.Errorf("cred = %+v", cred)
	}
}

func TestFileProvider_SelectByBotID(t *testing.T) {
	s := fileTestStore(t, map[string]string{"a": "cli_a", "b": "cli_b"})
	cred, err := NewFileProvider(s, "", "cli_b").Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Profile != "b" || cred.RobotID != "cli_b" {
		t.Errorf("cred = %+v", cred)
	}
}

func TestFileProvider_BothConsistent(t *testing.T) {
	s := fileTestStore(t, map[string]string{"a": "cli_a"})
	cred, err := NewFileProvider(s, "a", "cli_a").Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Profile != "a" {
		t.Errorf("cred = %+v", cred)
	}
}

func TestFileProvider_BothInconsistentErrors(t *testing.T) {
	s := fileTestStore(t, map[string]string{"a": "cli_a"})
	_, err := NewFileProvider(s, "a", "cli_b").Resolve()
	if got := exitType(t, err); got != "auth_error" {
		t.Errorf("type = %q, want auth_error", got)
	}
}

func TestFileProvider_BotIDNoMatchErrors(t *testing.T) {
	s := fileTestStore(t, map[string]string{"a": "cli_a"})
	_, err := NewFileProvider(s, "", "cli_zzz").Resolve()
	if got := exitType(t, err); got != "auth_error" {
		t.Errorf("type = %q, want auth_error", got)
	}
}

func TestFileProvider_NameNotFoundErrors(t *testing.T) {
	s := fileTestStore(t, map[string]string{"a": "cli_a"})
	_, err := NewFileProvider(s, "nope", "").Resolve()
	if got := exitType(t, err); got != "auth_error" {
		t.Errorf("type = %q, want auth_error", got)
	}
}

func TestFileProvider_AmbiguousErrors(t *testing.T) {
	s := fileTestStore(t, map[string]string{"a": "cli_a", "b": "cli_b"})
	_, err := NewFileProvider(s, "", "").Resolve()
	if got := exitType(t, err); got != "validation" {
		t.Errorf("type = %q, want validation", got)
	}
}

func TestFileProvider_ZeroProfilesFallsThrough(t *testing.T) {
	s := fileTestStore(t, nil)
	cred, err := NewFileProvider(s, "", "").Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred != nil {
		t.Errorf("expected nil cred to fall through to env, got %+v", cred)
	}
}

func TestFileProvider_ZeroProfilesWithExplicitBotIDErrors(t *testing.T) {
	s := fileTestStore(t, nil)
	_, err := NewFileProvider(s, "", "runtime-bot").Resolve()
	if got := exitType(t, err); got != "auth_error" {
		t.Errorf("type = %q, want auth_error", got)
	}
}

func TestFileProvider_ZeroProfilesWithEnvironmentBotIDFallsThrough(t *testing.T) {
	s := fileTestStore(t, nil)
	provider := NewFileProvider(s, "", "runtime-bot")
	provider.AllowEmptyStoreBotIDFallback = true
	cred, err := provider.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred != nil {
		t.Errorf("expected nil cred to fall through to env, got %+v", cred)
	}
}

func TestFileProvider_NilStore(t *testing.T) {
	cred, err := NewFileProvider(nil, "", "").Resolve()
	if err != nil || cred != nil {
		t.Errorf("nil store should yield (nil,nil), got cred=%+v err=%v", cred, err)
	}
}
