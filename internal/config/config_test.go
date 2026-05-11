package config

import (
	"testing"
)

func TestValidate_MissingToken(t *testing.T) {
	c := &Config{APIURL: "http://localhost", BotToken: ""}
	if err := c.Validate(); err == nil {
		t.Error("expected error for missing OCTO_BOT_TOKEN")
	}
}

func TestValidate_OK(t *testing.T) {
	c := &Config{APIURL: "http://localhost", BotToken: "app_xxx"}
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv(EnvBotToken, "")
	t.Setenv(EnvAPIURL, "")
	t.Setenv(EnvFormat, "")
	t.Setenv(EnvMattersURL, "")
	t.Setenv(EnvDmworkIMURL, "")
	t.Setenv(EnvSpaceID, "")

	cfg := Load()
	if cfg.APIURL != "http://127.0.0.1:8080" {
		t.Errorf("APIURL = %q, want default", cfg.APIURL)
	}
	if cfg.Format != "json" {
		t.Errorf("Format = %q, want json", cfg.Format)
	}
	if cfg.MattersURL != "" {
		t.Errorf("MattersURL = %q, want empty", cfg.MattersURL)
	}
}

func TestLoad_ReadsAllEnvVars(t *testing.T) {
	t.Setenv(EnvAPIURL, "http://api.example")
	t.Setenv(EnvMattersURL, "http://matters.example")
	t.Setenv(EnvDmworkIMURL, "http://im.example")
	t.Setenv(EnvBotToken, "app_xxx")
	t.Setenv(EnvSpaceID, "space-1")
	t.Setenv(EnvFormat, "table")

	cfg := Load()
	if cfg.APIURL != "http://api.example" {
		t.Errorf("APIURL = %q", cfg.APIURL)
	}
	if cfg.MattersURL != "http://matters.example" {
		t.Errorf("MattersURL = %q", cfg.MattersURL)
	}
	if cfg.DmworkIMURL != "http://im.example" {
		t.Errorf("DmworkIMURL = %q", cfg.DmworkIMURL)
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

func TestServiceURL_MattersOverride(t *testing.T) {
	cfg := &Config{APIURL: "http://api", MattersURL: "http://matters"}
	if got := cfg.ServiceURL("matters"); got != "http://matters" {
		t.Errorf("matters URL = %q, want override", got)
	}
}

func TestServiceURL_DmworkIMOverride(t *testing.T) {
	cfg := &Config{APIURL: "http://api", DmworkIMURL: "http://im"}
	if got := cfg.ServiceURL("dmworkim"); got != "http://im" {
		t.Errorf("dmworkim URL = %q, want override", got)
	}
}

func TestServiceURL_FallbackToAPIURL(t *testing.T) {
	cfg := &Config{APIURL: "http://api"}
	tests := []string{"matters", "dmworkim", "unknown"}
	for _, s := range tests {
		if got := cfg.ServiceURL(s); got != "http://api" {
			t.Errorf("ServiceURL(%q) = %q, want fallback", s, got)
		}
	}
}
