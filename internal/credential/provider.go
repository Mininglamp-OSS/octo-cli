// Package credential resolves bot credentials from one or more sources.
// Phase 1 supports environment variables only; future phases add config files
// and sidecar providers behind the same interface.
package credential

import (
	"errors"
	"fmt"
)

// BotCredential is the resolved App Bot credential. SpaceID is optional here:
// space-scoped bots have it resolved server-side; platform-scoped bots need
// --space or OCTO_SPACE_ID to populate it. Source is a human tag (e.g.
// "env:OCTO_BOT_TOKEN") used in verbose output and error messages.
//
// Profile is populated only for stored credentials. RobotID may come from a
// stored profile or OCTO_BOT_ID, while BotKind is derived from the token prefix.
// An OCTO_BOT_ID beside an environment token remains an unverified claim: it is
// emitted as robot_id_claimed rather than robot_id and can select only a Mail
// credential whose encrypted local binding matches the active token.
type BotCredential struct {
	Token   string
	SpaceID string
	Source  string

	Profile string
	RobotID string
	BotKind string
}

// Source resolves a credential from a single source. Implementations should
// return a nil credential and no error when the source is simply absent; the
// chain treats that as "move on to the next provider". An error means the
// source is present but malformed / unreadable and the chain should stop.
type Source interface {
	Name() string
	Resolve() (*BotCredential, error)
}

// ErrNoCredential is wrapped into the error returned when every source in the
// chain reports absent. Callers can errors.Is it to distinguish "nothing
// configured" (fall back / prompt for a token) from a structured resolution
// failure (which must surface).
var ErrNoCredential = errors.New("no credential found")

// Provider is a chain of credential sources tried in order. The first source
// that yields a non-nil credential wins.
type Provider struct {
	providers []Source
}

// NewChain builds a chain. Sources are consulted in the given order.
func NewChain(providers ...Source) *Provider {
	return &Provider{providers: providers}
}

// Resolve walks the chain. It returns the first credential found, or an error
// listing every source it tried when none match. Errors from individual
// sources short-circuit the chain.
func (c *Provider) Resolve() (*BotCredential, error) {
	if c == nil || len(c.providers) == 0 {
		return nil, errors.New("no credential providers configured")
	}
	var sources []string
	for _, p := range c.providers {
		cred, err := p.Resolve()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name(), err)
		}
		if cred != nil {
			return cred, nil
		}
		sources = append(sources, p.Name())
	}
	return nil, fmt.Errorf("%w (tried: %v)", ErrNoCredential, sources)
}
