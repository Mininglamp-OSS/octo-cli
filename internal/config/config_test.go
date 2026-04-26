package config

import (
	"testing"
)

func TestValidate_MissingToken(t *testing.T) {
	c := &Config{APIURL: "http://localhost", BotToken: "", SpaceID: "sp-1"}
	if err := c.Validate(); err == nil {
		t.Error("expected error for missing OCTO_BOT_TOKEN")
	}
}

func TestValidate_MissingSpaceID(t *testing.T) {
	c := &Config{APIURL: "http://localhost", BotToken: "bot1/key1", SpaceID: ""}
	if err := c.Validate(); err == nil {
		t.Error("expected error for missing OCTO_SPACE_ID")
	}
}

func TestValidate_OK(t *testing.T) {
	c := &Config{APIURL: "http://localhost", BotToken: "bot1/key1", SpaceID: "sp-1"}
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "")
	t.Setenv("OCTO_API_URL", "")
	t.Setenv("OCTO_FORMAT", "")
	t.Setenv("OCTO_SPACE_ID", "")

	cfg := Load()
	if cfg.APIURL != "http://127.0.0.1:8080" {
		t.Errorf("APIURL = %q, want default", cfg.APIURL)
	}
	if cfg.Format != "json" {
		t.Errorf("Format = %q, want json", cfg.Format)
	}
}
