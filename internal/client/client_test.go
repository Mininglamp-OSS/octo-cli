package client

import (
	"testing"

	"github.com/dmwork-org/octo-cli/internal/config"
)

func TestNew_SetsFields(t *testing.T) {
	cfg := &config.Config{
		APIURL:   "http://localhost:8080",
		BotToken: "robot1/key123",
		SpaceID:  "sp-test",
	}

	c := New(cfg)
	if c.baseURL != cfg.APIURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, cfg.APIURL)
	}
	if c.botToken != cfg.BotToken {
		t.Errorf("botToken = %q, want %q", c.botToken, cfg.BotToken)
	}
	if c.spaceID != cfg.SpaceID {
		t.Errorf("spaceID = %q, want %q", c.spaceID, cfg.SpaceID)
	}
}
