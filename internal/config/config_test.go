package config

import (
	"testing"
)

func TestValidate_MissingToken(t *testing.T) {
	c := &Config{APIBaseURL: "http://localhost", BotToken: ""}
	if err := c.Validate(); err == nil {
		t.Error("expected error for missing OCTO_BOT_TOKEN")
	}
}

func TestValidate_OK(t *testing.T) {
	c := &Config{APIBaseURL: "http://localhost", BotToken: "app_xxx"}
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_CredentialMode(t *testing.T) {
	if err := (&Config{BotToken: "octo_loop_task", CredentialMode: CredentialModeTask}).Validate(); err != nil {
		t.Fatalf("task mode should validate: %v", err)
	}
	if err := (&Config{BotToken: "token", CredentialMode: "unknown"}).Validate(); err == nil {
		t.Fatal("unknown credential mode should fail validation")
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv(EnvToken, "")
	t.Setenv(EnvBotToken, "")
	t.Setenv(EnvAPIBaseURL, "")
	t.Setenv(EnvFormat, "")
	t.Setenv(EnvSpaceID, "")
	t.Setenv(EnvCredentialMode, "")

	cfg := Load()
	if cfg.APIBaseURL != DefaultAPIBaseURL {
		t.Errorf("APIBaseURL = %q, want default", cfg.APIBaseURL)
	}
	if cfg.Format != "json" {
		t.Errorf("Format = %q, want json", cfg.Format)
	}
}

func TestLoad_ReadsAllEnvVars(t *testing.T) {
	t.Setenv(EnvAPIBaseURL, "http://api.example")
	// EnvToken outranks EnvBotToken, so it must be cleared for this case to
	// exercise the EnvBotToken path rather than an exported real token.
	t.Setenv(EnvToken, "")
	t.Setenv(EnvBotToken, "app_xxx")
	t.Setenv(EnvSpaceID, "space-1")
	t.Setenv(EnvFormat, "table")
	t.Setenv(EnvCredentialMode, CredentialModeTask)

	cfg := Load()
	if cfg.APIBaseURL != "http://api.example" {
		t.Errorf("APIBaseURL = %q", cfg.APIBaseURL)
	}
	if cfg.BotToken != "app_xxx" {
		t.Errorf("BotToken = %q", cfg.BotToken)
	}
	if cfg.CredentialMode != CredentialModeTask {
		t.Errorf("CredentialMode = %q", cfg.CredentialMode)
	}
	if cfg.SpaceID != "space-1" {
		t.Errorf("SpaceID = %q", cfg.SpaceID)
	}
	if cfg.Format != "table" {
		t.Errorf("Format = %q", cfg.Format)
	}
}

func TestServiceURL_Unified(t *testing.T) {
	cfg := &Config{APIBaseURL: "http://api.example"}
	// All services should return the same API base URL.
	for _, svc := range []string{"matters", "dmworkim", "unknown", ""} {
		if got := cfg.ServiceURL(svc); got != "http://api.example" {
			t.Errorf("ServiceURL(%q) = %q, want API base URL", svc, got)
		}
	}
}

// OCTO_TOKEN is the high-priority token slot: it wins over OCTO_BOT_TOKEN, so a
// real-person user API key can be supplied for one command without disturbing an
// existing bot setup. An existing setup that never sets it is unaffected.
func TestLoad_TokenPrecedence(t *testing.T) {
	cases := []struct {
		name, octoToken, botToken, want string
	}{
		{"only the legacy variable", "", "bf_bot", "bf_bot"},
		{"only the new variable", "uk_person", "", "uk_person"},
		{"both set: the new one wins", "uk_person", "bf_bot", "uk_person"},
		{"a blank new variable does not shadow", "   ", "bf_bot", "bf_bot"},
		{"neither set", "", "", ""},
		// Both variables are trimmed, symmetrically. An untrimmed OCTO_BOT_TOKEN
		// holding only whitespace used to pass Validate here and then resolve to no
		// credential in credential.EnvProvider, which trims both — an auth failure
		// two layers away from its cause.
		{"a whitespace-only legacy variable is no credential", "", "   ", ""},
		{"a whitespace-only legacy variable with a tab", "", "\t\n", ""},
		{"surrounding whitespace is stripped from the legacy variable", "", "  bf_bot\n", "bf_bot"},
		{"surrounding whitespace is stripped from the new variable", "  uk_person\n", "", "uk_person"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvToken, tc.octoToken)
			t.Setenv(EnvBotToken, tc.botToken)
			if got := Load().BotToken; got != tc.want {
				t.Errorf("BotToken = %q, want %q", got, tc.want)
			}
		})
	}
}

// The auth gate's message must name both variables, or an operator who only set
// OCTO_TOKEN gets told to set a variable they already decided against.
func TestValidate_MessageNamesBothTokenVars(t *testing.T) {
	err := (&Config{}).Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{EnvToken, EnvBotToken} {
		if !contains(err.Error(), want) {
			t.Errorf("Validate message %q should name %s", err.Error(), want)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
