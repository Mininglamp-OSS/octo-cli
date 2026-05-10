// Package config loads CLI configuration from environment variables.
// Per architecture §3.2 the Octo ecosystem is multi-service — matters and
// dmworkim may live at different URLs — so Config supports per-service overrides
// on top of a single fallback OCTO_API_URL.
package config

import (
	"fmt"
	"os"
)

// Environment variable names. Centralised for testability and discoverability.
const (
	EnvAPIURL      = "OCTO_API_URL"
	EnvMattersURL  = "OCTO_MATTERS_URL"
	EnvDmworkIMURL = "OCTO_DMWORKIM_URL"
	EnvBotToken    = "OCTO_BOT_TOKEN"
	EnvSpaceID    = "OCTO_SPACE_ID"
	EnvFormat      = "OCTO_FORMAT"
)

// Config holds CLI configuration loaded from environment variables.
// BotToken and SpaceID live here too (duplicated in credential.EnvProvider) so
// callers wiring a client without a full provider chain still have access; the
// credential provider remains the authoritative path for command execution.
type Config struct {
	// APIURL is the fallback base URL used when a service has no dedicated override.
	APIURL string
	// MattersURL overrides the matters-service base URL (OCTO_MATTERS_URL).
	MattersURL string
	// DmworkIMURL overrides the dmworkim base URL (OCTO_DMWORKIM_URL).
	DmworkIMURL string
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
		APIURL:      envOrDefault(EnvAPIURL, "http://127.0.0.1:8080"),
		MattersURL:  os.Getenv(EnvMattersURL),
		DmworkIMURL: os.Getenv(EnvDmworkIMURL),
		BotToken:    os.Getenv(EnvBotToken),
		SpaceID:     os.Getenv(EnvSpaceID),
		Format:      envOrDefault(EnvFormat, "json"),
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

// ServiceURL returns the base URL for the named service. Explicit per-service
// overrides win; otherwise the generic APIURL is returned. Unknown service
// names fall through to APIURL so the client doesn't panic on future domains.
func (c *Config) ServiceURL(service string) string {
	switch service {
	case "matters":
		if c.MattersURL != "" {
			return c.MattersURL
		}
	case "dmworkim":
		if c.DmworkIMURL != "" {
			return c.DmworkIMURL
		}
	}
	return c.APIURL
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
