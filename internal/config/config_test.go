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

func TestLoad_Defaults(t *testing.T) {
	t.Setenv(EnvBotToken, "")
	t.Setenv(EnvAPIBaseURL, "")
	t.Setenv(EnvFormat, "")
	t.Setenv(EnvSpaceID, "")

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
	t.Setenv(EnvBotToken, "app_xxx")
	t.Setenv(EnvSpaceID, "space-1")
	t.Setenv(EnvFormat, "table")

	cfg := Load()
	if cfg.APIBaseURL != "http://api.example" {
		t.Errorf("APIBaseURL = %q", cfg.APIBaseURL)
	}
	if cfg.BotToken != "app_xxx" {
		t.Errorf("BotToken = %q", cfg.BotToken)
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
