package credential

import (
	"errors"
	"strings"
	"testing"
)

// --- EnvProvider ---

func TestEnvProvider_ResolvesFromEnv(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "app_xxx")
	t.Setenv("OCTO_SPACE_ID", "space-1")

	cred, err := NewEnvProvider().Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred == nil {
		t.Fatal("cred is nil")
	}
	if cred.Token != "app_xxx" {
		t.Errorf("Token = %q", cred.Token)
	}
	if cred.SpaceID != "space-1" {
		t.Errorf("SpaceID = %q", cred.SpaceID)
	}
	if cred.Source != "env:OCTO_BOT_TOKEN" {
		t.Errorf("Source = %q", cred.Source)
	}
}

func TestEnvProvider_NoTokenReturnsNil(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "")

	cred, err := NewEnvProvider().Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred != nil {
		t.Errorf("cred should be nil when env var unset, got %+v", cred)
	}
}

func TestEnvProvider_TrimsWhitespace(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "  app_xxx  ")

	cred, err := NewEnvProvider().Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Token != "app_xxx" {
		t.Errorf("Token should be trimmed, got %q", cred.Token)
	}
}

func TestEnvProvider_Name(t *testing.T) {
	p := NewEnvProvider()
	if p.Name() != "env:OCTO_BOT_TOKEN" {
		t.Errorf("Name = %q", p.Name())
	}
	p2 := &EnvProvider{TokenVar: "CUSTOM"}
	if p2.Name() != "env:CUSTOM" {
		t.Errorf("Name = %q", p2.Name())
	}
}

// --- Chain ---

type stubProvider struct {
	name string
	cred *BotCredential
	err  error
}

func (s *stubProvider) Name() string                     { return s.name }
func (s *stubProvider) Resolve() (*BotCredential, error) { return s.cred, s.err }

func TestChain_FirstProviderWins(t *testing.T) {
	p1 := &stubProvider{name: "p1", cred: &BotCredential{Token: "a", Source: "p1"}}
	p2 := &stubProvider{name: "p2", cred: &BotCredential{Token: "b", Source: "p2"}}

	cred, err := NewChain(p1, p2).Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Token != "a" {
		t.Errorf("Token = %q, want a", cred.Token)
	}
}

func TestChain_FallsThroughOnNil(t *testing.T) {
	p1 := &stubProvider{name: "p1"} // returns nil
	p2 := &stubProvider{name: "p2", cred: &BotCredential{Token: "b", Source: "p2"}}

	cred, err := NewChain(p1, p2).Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Token != "b" {
		t.Errorf("Token = %q, want b", cred.Token)
	}
}

func TestChain_NoneFoundErrorListsSources(t *testing.T) {
	p1 := &stubProvider{name: "p1"}
	p2 := &stubProvider{name: "p2"}

	_, err := NewChain(p1, p2).Resolve()
	if err == nil {
		t.Fatal("expected error when no provider yields cred")
	}
	if !strings.Contains(err.Error(), "p1") || !strings.Contains(err.Error(), "p2") {
		t.Errorf("error should list tried sources, got: %v", err)
	}
}

func TestChain_ProviderErrorShortCircuits(t *testing.T) {
	p1 := &stubProvider{name: "p1", err: errors.New("malformed")}
	p2 := &stubProvider{name: "p2", cred: &BotCredential{Token: "b"}}

	_, err := NewChain(p1, p2).Resolve()
	if err == nil {
		t.Fatal("expected error to propagate from p1")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error missing original cause: %v", err)
	}
}

func TestChain_EmptyErrors(t *testing.T) {
	_, err := NewChain().Resolve()
	if err == nil {
		t.Error("expected error for empty chain")
	}
}
