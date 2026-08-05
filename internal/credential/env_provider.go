package credential

import (
	"os"
	"strings"
)

// EnvProvider reads the bot credential from environment variables.
// OCTO_BOT_TOKEN holds an Octo or Loop bearer credential.
// OCTO_SPACE_ID is optional and only required for platform-scoped bots.
type EnvProvider struct {
	// TokenVar is the env var holding the token. Defaults to OCTO_BOT_TOKEN.
	TokenVar string
	// SpaceVar is the env var holding the space id. Defaults to OCTO_SPACE_ID.
	SpaceVar string
}

// NewEnvProvider builds an EnvProvider with default variable names.
func NewEnvProvider() *EnvProvider {
	return &EnvProvider{TokenVar: "OCTO_BOT_TOKEN", SpaceVar: "OCTO_SPACE_ID"}
}

// Name implements Source.
func (e *EnvProvider) Name() string {
	v := e.TokenVar
	if v == "" {
		v = "OCTO_BOT_TOKEN"
	}
	return "env:" + v
}

// Resolve reads the configured env vars. Returns a nil credential and nil
// error when the token is absent — the chain treats that as "not my turn".
func (e *EnvProvider) Resolve() (*BotCredential, error) {
	tokenVar := e.TokenVar
	if tokenVar == "" {
		tokenVar = "OCTO_BOT_TOKEN"
	}
	spaceVar := e.SpaceVar
	if spaceVar == "" {
		spaceVar = "OCTO_SPACE_ID"
	}

	token := strings.TrimSpace(os.Getenv(tokenVar))
	if token == "" {
		return nil, nil
	}
	return &BotCredential{
		Token:   token,
		SpaceID: strings.TrimSpace(os.Getenv(spaceVar)),
		Source:  "env:" + tokenVar,
	}, nil
}
