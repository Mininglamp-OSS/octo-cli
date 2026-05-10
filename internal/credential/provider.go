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
type BotCredential struct {
	Token   string
	SpaceID string
	Source  string
}

// Provider resolves a credential from a single source. Implementations should
// return a nil credential and no error when the source is simply absent; the
// chain treats that as "move on to the next provider". An error means the
// source is present but malformed / unreadable and the chain should stop.
type Provider interface {
	Name() string
	Resolve() (*BotCredential, error)
}

// CredentialProvider is a chain of providers tried in order. The first
// provider that yields a non-nil credential wins.
type CredentialProvider struct {
	providers []Provider
}

// NewChain builds a chain. Providers are consulted in the given order.
func NewChain(providers ...Provider) *CredentialProvider {
	return &CredentialProvider{providers: providers}
}

// Resolve walks the chain. It returns the first credential found, or an error
// listing every source it tried when none match. Errors from individual
// providers short-circuit the chain.
func (c *CredentialProvider) Resolve() (*BotCredential, error) {
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
	return nil, fmt.Errorf("no credential found (tried: %v)", sources)
}
