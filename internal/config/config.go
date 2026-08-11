// Package config loads CLI configuration from environment variables.
// All backend services are accessed through a single API base URL
// (OCTO_API_BASE_URL).
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Environment variable names. Centralised for testability and discoverability.
const (
	EnvAPIBaseURL = "OCTO_API_BASE_URL"
	// EnvToken is the preferred token variable. It holds any of the three token
	// kinds (app_*, bf_*, uk_*) and takes precedence over EnvBotToken, so a
	// real-person user API key can be supplied without disturbing an existing
	// OCTO_BOT_TOKEN setup.
	EnvToken    = "OCTO_TOKEN"
	EnvBotToken = "OCTO_BOT_TOKEN"
	// EnvCredentialMode selects the credential resolution policy. The empty
	// value keeps the normal profile/bot flow; "task" selects the restricted
	// credential policy used by correctly isolated octo-daemon task processes.
	EnvCredentialMode  = "OCTO_CREDENTIAL_MODE"
	CredentialModeTask = "task"
	EnvSpaceID         = "OCTO_SPACE_ID"
	EnvFormat          = "OCTO_FORMAT"
	// DefaultAPIBaseURL is the production Octo gateway. Service operations
	// append their registered paths (for example, /v1/bot/groups).
	DefaultAPIBaseURL = "https://im.deepminer.com.cn"
	// EnvBotID is the env form of --bot-id: a robot id that selects a stored
	// credential profile. It is a selector, not a secret (cf. EnvBotToken).
	EnvBotID = "OCTO_BOT_ID"
)

// Config holds CLI configuration loaded from environment variables.
// BotToken and SpaceID live here too (duplicated in credential.EnvProvider) so
// callers wiring a client without a full provider chain still have access; the
// credential provider remains the authoritative path for command execution.
type Config struct {
	// APIBaseURL is the unified base URL for all backend services.
	// Set via OCTO_API_BASE_URL.
	APIBaseURL string
	// BotToken is the caller's token (OCTO_TOKEN, else OCTO_BOT_TOKEN).
	BotToken string
	// CredentialMode controls credential selection, not server authorization or
	// process isolation. Daemon task processes must use an isolated config dir.
	// Fleet remains authoritative for the bearer credential's principal kind,
	// bindings, and actions.
	CredentialMode string
	// SpaceID is the platform-bot space context (OCTO_SPACE_ID). Optional for space-scoped bots.
	SpaceID string
	// Format is the default output format.
	Format string
}

// Load reads configuration from the environment. The token follows the same
// precedence as credential.EnvProvider — OCTO_TOKEN wins over OCTO_BOT_TOKEN —
// so the auth gate and `config show` agree with the resolved credential.
func Load() *Config {
	return &Config{
		APIBaseURL:     envOrDefault(EnvAPIBaseURL, DefaultAPIBaseURL),
		BotToken:       envToken(),
		CredentialMode: strings.ToLower(strings.TrimSpace(os.Getenv(EnvCredentialMode))),
		SpaceID:        os.Getenv(EnvSpaceID),
		Format:         envOrDefault(EnvFormat, "json"),
	}
}

// envToken returns the token from the highest-precedence variable that is set.
// Both variables are trimmed, and symmetrically: an untrimmed OCTO_BOT_TOKEN
// holding only whitespace would pass Validate here and then resolve to no
// credential in credential.EnvProvider, which trims both.
func envToken() string {
	if v := strings.TrimSpace(os.Getenv(EnvToken)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(EnvBotToken))
}

// Validate checks required fields. Only the token is required at load time;
// space-id validation is deferred to the client (which knows whether the bot
// is space- or platform-scoped from the spec).
func (c *Config) Validate() error {
	if c.CredentialMode != "" && c.CredentialMode != CredentialModeTask {
		return fmt.Errorf("unsupported %s %q", EnvCredentialMode, c.CredentialMode)
	}
	if c.BotToken == "" {
		return fmt.Errorf("%s or %s is required (app_*, bf_*, uk_*, or octo_loop_* token)", EnvToken, EnvBotToken)
	}
	if c.APIBaseURL != "" {
		baseURL, err := NormalizeAPIBaseURL(c.APIBaseURL)
		if err != nil {
			return err
		}
		c.APIBaseURL = baseURL
	}
	return nil
}

// NormalizeAPIBaseURL validates the unified Octo gateway base URL. The CLI
// stores only the origin and appends module-qualified paths per operation.
func NormalizeAPIBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid %s: %w", EnvAPIBaseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s must use http or https", EnvAPIBaseURL)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("%s must be an absolute base URL without credentials, query parameters, or fragments", EnvAPIBaseURL)
	}
	if strings.TrimRight(u.Path, "/") != "" {
		return "", fmt.Errorf("%s must not include a service or API path", EnvAPIBaseURL)
	}
	u.Path = ""
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// ServiceURL returns the base URL for the named service. With the unified
// API base URL model all services share the same URL — this method exists
// for interface compatibility so the client and service engine don't need
// to change their routing logic.
func (c *Config) ServiceURL(_ string) string {
	return c.APIBaseURL
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
