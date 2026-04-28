package config

import (
	"fmt"
	"os"
)

// Config holds CLI configuration loaded from environment variables.
type Config struct {
	APIURL   string // OCTO_API_URL — base URL of todo-service
	BotToken string // OCTO_BOT_TOKEN — BotFather bot_token (Bearer auth)
	SpaceID  string // OCTO_SPACE_ID — optional, auto-resolved from bot auth if not set
	Format   string // output format: "json" (default) or "table"
}

// Load reads configuration from environment variables.
func Load() *Config {
	return &Config{
		APIURL:   envOrDefault("OCTO_API_URL", "http://127.0.0.1:8080"),
		BotToken: os.Getenv("OCTO_BOT_TOKEN"),
		SpaceID:  os.Getenv("OCTO_SPACE_ID"),
		Format:   envOrDefault("OCTO_FORMAT", "json"),
	}
}

// Validate checks required fields.
func (c *Config) Validate() error {
	if c.BotToken == "" {
		return fmt.Errorf("OCTO_BOT_TOKEN is required (BotFather bot_token)")
	}
	if c.SpaceID == "" {
		// SpaceID is optional; bot auth can auto-resolve from verify-bot response.
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
