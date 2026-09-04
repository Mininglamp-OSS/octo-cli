package credential

import (
	"os"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
)

// EnvProvider reads the bot credential from environment variables.
// Token aliases are normalized by config.ResolveEnvToken before this provider
// builds the internal credential. OCTO_SPACE_ID is optional and only required
// for platform-scoped bots.
type EnvProvider struct {
	// SpaceVar is the env var holding the space id. Defaults to OCTO_SPACE_ID.
	SpaceVar string
}

// NewEnvProvider builds an EnvProvider with default variable names.
func NewEnvProvider() *EnvProvider {
	return &EnvProvider{SpaceVar: "OCTO_SPACE_ID"}
}

// Name implements Source. It lists both accepted aliases in precedence order.
func (e *EnvProvider) Name() string {
	return "env:" + config.EnvToken + "/" + config.EnvBotToken
}

// Resolve reads the configured env vars. Returns a nil credential and nil
// error when the token is absent — the chain treats that as "not my turn".
// Source records the alias selected at the config boundary, so the envelope's
// identity.source remains useful without leaking alias handling downstream.
func (e *EnvProvider) Resolve() (*BotCredential, error) {
	spaceVar := e.SpaceVar
	if spaceVar == "" {
		spaceVar = "OCTO_SPACE_ID"
	}

	resolved := config.ResolveEnvToken()
	if resolved.Token == "" {
		return nil, nil
	}
	return &BotCredential{
		Token:   resolved.Token,
		SpaceID: strings.TrimSpace(os.Getenv(spaceVar)),
		Source:  resolved.Source,
	}, nil
}
