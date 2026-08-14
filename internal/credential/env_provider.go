package credential

import (
	"os"
	"strings"
)

// defaultTokenVars is the ordered token-variable preference. OCTO_TOKEN is the
// high-priority slot for any token kind (app_*, bf_*, uk_*); OCTO_BOT_TOKEN is
// the historical variable and stays fully supported, so an existing setup that
// never sets OCTO_TOKEN behaves exactly as before.
var defaultTokenVars = []string{"OCTO_TOKEN", "OCTO_BOT_TOKEN"}

// EnvProvider reads the bot credential from environment variables.
// The token comes from the first non-empty variable in TokenVars
// (OCTO_TOKEN, then OCTO_BOT_TOKEN). OCTO_BOT_ID optionally asserts the Bot
// that owns the token. OCTO_SPACE_ID is optional and only required for
// platform-scoped bots.
type EnvProvider struct {
	// TokenVar pins the provider to a single env var, overriding TokenVars.
	// Kept for callers that need an explicit variable.
	TokenVar string
	// TokenVars is the ordered variable preference; the first non-empty one
	// wins. Empty means the default OCTO_TOKEN → OCTO_BOT_TOKEN order.
	TokenVars []string
	// BotIDVar is the env var holding the Bot id. Defaults to OCTO_BOT_ID.
	BotIDVar string
	// SpaceVar is the env var holding the space id. Defaults to OCTO_SPACE_ID.
	SpaceVar string
}

// NewEnvProvider builds an EnvProvider with default variable names.
func NewEnvProvider() *EnvProvider {
	return &EnvProvider{
		TokenVars: defaultTokenVars,
		BotIDVar:  "OCTO_BOT_ID",
		SpaceVar:  "OCTO_SPACE_ID",
	}
}

// tokenVars returns the ordered variables this provider consults.
func (e *EnvProvider) tokenVars() []string {
	if e.TokenVar != "" {
		return []string{e.TokenVar}
	}
	if len(e.TokenVars) > 0 {
		return e.TokenVars
	}
	return defaultTokenVars
}

// Name implements Source. It lists every variable the provider consults so the
// "no credential found" error names all of them.
func (e *EnvProvider) Name() string {
	return "env:" + strings.Join(e.tokenVars(), "/")
}

// Resolve reads the configured env vars. Returns a nil credential and nil
// error when the token is absent — the chain treats that as "not my turn".
// Source records the variable the token actually came from, so the envelope's
// identity.source distinguishes env:OCTO_TOKEN from env:OCTO_BOT_TOKEN.
func (e *EnvProvider) Resolve() (*BotCredential, error) {
	spaceVar := e.SpaceVar
	if spaceVar == "" {
		spaceVar = "OCTO_SPACE_ID"
	}
	botIDVar := e.BotIDVar
	if botIDVar == "" {
		botIDVar = "OCTO_BOT_ID"
	}

	for _, tokenVar := range e.tokenVars() {
		token := strings.TrimSpace(os.Getenv(tokenVar))
		if token == "" {
			continue
		}
		return &BotCredential{
			Token:   token,
			SpaceID: strings.TrimSpace(os.Getenv(spaceVar)),
			Source:  "env:" + tokenVar,
			RobotID: strings.TrimSpace(os.Getenv(botIDVar)),
			BotKind: TokenKind(token),
		}, nil
	}
	return nil, nil
}
