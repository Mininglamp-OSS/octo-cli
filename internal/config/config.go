// Package config loads CLI configuration from environment variables.
// All backend services are accessed through a single API base URL
// (OCTO_API_BASE_URL).
package config

import (
	"fmt"
	"os"
)

// Environment variable names. Centralised for testability and discoverability.
const (
	EnvAPIBaseURL = "OCTO_API_BASE_URL"
	EnvBotToken   = "OCTO_BOT_TOKEN"
	EnvSpaceID    = "OCTO_SPACE_ID"
	EnvFormat     = "OCTO_FORMAT"
)

// Config holds CLI configuration loaded from environment variables.
// BotToken and SpaceID live here too (duplicated in credential.EnvProvider) so
// callers wiring a client without a full provider chain still have access; the
// credential provider remains the authoritative path for command execution.
type Config struct {
	// APIBaseURL is the unified base URL for all backend services.
	// Set via OCTO_API_BASE_URL.
	APIBaseURL string
	// BotToken is the App Bot token (OCTO_BOT_TOKEN).
	BotToken string
	// SpaceID is the platform-bot space context (OCTO_SPACE_ID). Optional for space-scoped bots.
	SpaceID string
	// Format is the default output format.
	Format string
}

// Load reads configuration from the environment.
func Load() *Config {
	return &Config{
		APIBaseURL: envOrDefault(EnvAPIBaseURL, "http://127.0.0.1:8080"),
		BotToken:   os.Getenv(EnvBotToken),
		SpaceID:    os.Getenv(EnvSpaceID),
		Format:     envOrDefault(EnvFormat, "json"),
	}
}

// Validate checks required fields. Only the token is required at load time;
// space-id validation is deferred to the client (which knows whether the bot
// is space- or platform-scoped from the spec).
func (c *Config) Validate() error {
	if c.BotToken == "" {
		return fmt.Errorf("%s is required (App Bot token, app_*)", EnvBotToken)
	}
	return nil
}

// ServiceURL returns the base URL for the named service. With the unified
// API base URL model all services share the same URL — this method exists
// for interface compatibility so the client and service engine don't need
// to change their routing logic.
func (c *Config) ServiceURL(service string) string {
	return c.APIBaseURL
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
