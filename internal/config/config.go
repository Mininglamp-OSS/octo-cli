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
	// EnvToken is the canonical token variable. It accepts every supported
	// credential kind and takes precedence over the compatible EnvBotToken alias.
	EnvToken    = "OCTO_TOKEN"
	EnvBotToken = "OCTO_BOT_TOKEN"
	// EnvTaskToken carries the daemon-issued Loop task credential separately
	// from native Octo credentials. It is consumed only by task-mode Loop commands.
	EnvTaskToken = "OCTO_TASK_TOKEN"
	// EnvCredentialMode selects the credential resolution policy. The empty
	// value keeps the normal profile/bot flow; "task" is a fail-closed mode
	// selected explicitly for an individual task-workflow CLI invocation.
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
// BotToken and SpaceID live here so callers wiring a client without a full
// provider chain still have access; the credential provider remains the
// authoritative path for command execution.
type Config struct {
	// APIBaseURL is the unified base URL for all backend services.
	// Set via OCTO_API_BASE_URL or the active profile's stored origin.
	APIBaseURL string
	// APIBaseURLSource reports where APIBaseURL came from: "profile", "env",
	// or "default". It makes the precedence rule observable in diagnostics.
	APIBaseURLSource string
	// BotToken is the active Octo or Loop bearer credential.
	BotToken string
	// CredentialMode controls credential selection, not server authorization.
	// Fleet remains authoritative for the bearer credential's principal kind,
	// bindings, and actions.
	CredentialMode string
	// SpaceID is the platform-bot space context (OCTO_SPACE_ID). Optional for space-scoped bots.
	SpaceID string
	// Format is the default output format.
	Format string
}

// ResolvedEnvToken is the canonical credential value resolved at the process
// environment boundary. Callers use Token and Source without knowing which
// compatibility alias supplied it.
type ResolvedEnvToken struct {
	Token  string
	Source string
}

// Load reads configuration from the environment. Token aliases are normalized
// by ResolveEnvToken so the auth gate and credential provider cannot drift.
func Load() *Config {
	token := ResolveEnvToken()
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(EnvCredentialMode)))
	spaceID := os.Getenv(EnvSpaceID)
	if mode == CredentialModeTask {
		spaceID = ""
	}
	apiBaseURL := os.Getenv(EnvAPIBaseURL)
	apiBaseURLSource := "env"
	if apiBaseURL == "" {
		apiBaseURL = DefaultAPIBaseURL
		apiBaseURLSource = "default"
	}
	return &Config{
		APIBaseURL:       apiBaseURL,
		APIBaseURLSource: apiBaseURLSource,
		BotToken:         token.Token,
		CredentialMode:   mode,
		SpaceID:          spaceID,
		Format:           envOrDefault(EnvFormat, "json"),
	}
}

// ResolveEnvToken selects the explicit credential mode at the environment
// boundary. Task mode never reads or falls back to ordinary token aliases.
func ResolveEnvToken() ResolvedEnvToken {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(EnvCredentialMode)), CredentialModeTask) {
		if v := strings.TrimSpace(os.Getenv(EnvTaskToken)); v != "" {
			return ResolvedEnvToken{Token: v, Source: "env:" + EnvTaskToken}
		}
		return ResolvedEnvToken{}
	}
	if v := strings.TrimSpace(os.Getenv(EnvToken)); v != "" {
		return ResolvedEnvToken{Token: v, Source: "env:" + EnvToken}
	}
	if v := strings.TrimSpace(os.Getenv(EnvBotToken)); v != "" {
		return ResolvedEnvToken{Token: v, Source: "env:" + EnvBotToken}
	}
	return ResolvedEnvToken{}
}

// Validate checks required fields. Only the token is required at load time;
// space-id validation is deferred to the client (which knows whether the bot
// is space- or platform-scoped from the spec).
func (c *Config) Validate() error {
	if c.CredentialMode != "" && c.CredentialMode != CredentialModeTask {
		return fmt.Errorf("unsupported %s %q", EnvCredentialMode, c.CredentialMode)
	}
	if c.BotToken == "" {
		if c.CredentialMode == CredentialModeTask {
			return fmt.Errorf("%s is required in task mode", EnvTaskToken)
		}
		return fmt.Errorf("%s or %s is required (Octo or Loop bearer credential)", EnvToken, EnvBotToken)
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
