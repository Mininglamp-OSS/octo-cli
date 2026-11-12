package permission

import (
	"context"
	"sync/atomic"
)

// DirectedCatalogPair is a private reviewed direction, never an inferred reverse.
type DirectedCatalogPair struct{ From, To CatalogPin }
type catalogRegistryState struct {
	catalogs map[CatalogPin]*VerifiedCatalog
	pairs    map[DirectedCatalogPair]bool
}

// CatalogRegistry atomically publishes complete immutable privately supported sets.
// The constructor's pins/pairs are trusted composition inputs, not operator/model
// hashes. Production startup retains an empty compiled list until source/bounds
// and independent support gates close; tests supply explicit synthetic pins.
type CatalogRegistry struct {
	approved map[CatalogPin]bool
	pairs    []DirectedCatalogPair
	current  atomic.Pointer[catalogRegistryState]
}

// NewCatalogRegistry freezes the exact independently approved support inputs.
func NewCatalogRegistry(pins []CatalogPin, pairs []DirectedCatalogPair) (*CatalogRegistry, error) {
	registry := &CatalogRegistry{approved: map[CatalogPin]bool{}, pairs: append([]DirectedCatalogPair(nil), pairs...)}
	versions := map[string]bool{}
	for _, pin := range pins {
		if !asciiToken(pin.Version, 64, false) || !validHex(pin.Digest, 64) || versions[pin.Version] || registry.approved[pin] {
			return nil, ErrInvalid
		}
		versions[pin.Version] = true
		registry.approved[pin] = true
	}
	seen := map[DirectedCatalogPair]bool{}
	for _, pair := range pairs {
		if !registry.approved[pair.From] || !registry.approved[pair.To] || pair.From == pair.To || seen[pair] {
			return nil, ErrInvalid
		}
		seen[pair] = true
	}
	return registry, nil
}

// Install validates the entire proposed set and directed equivalence before swap.
// An error leaves the earlier snapshot intact; it never makes a partial set live.
func (r *CatalogRegistry) Install(ctx context.Context, snapshots []*VerifiedCatalog) error {
	if len(snapshots) != len(r.approved) {
		return ErrUnavailable
	}
	state := &catalogRegistryState{catalogs: map[CatalogPin]*VerifiedCatalog{}, pairs: map[DirectedCatalogPair]bool{}}
	for _, v := range snapshots {
		if err := ctx.Err(); err != nil {
			return err
		}
		if v == nil || !r.approved[v.pin] || state.catalogs[v.pin] != nil || len(v.semantic) == 0 || catalogHash(v.files["manifest.json"]) != v.pin.Digest {
			return ErrUnavailable
		}
		state.catalogs[v.pin] = v
	}
	for _, pair := range r.pairs {
		if !state.catalogs[pair.From].SameSemantics(state.catalogs[pair.To]) {
			return ErrConflict
		}
		state.pairs[pair] = true
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.current.Store(state)
	return nil
}

// Lookup requires the exact pin from fresh complete P; there is no latest fallback.
func (r *CatalogRegistry) Lookup(pin CatalogPin) (*VerifiedCatalog, bool) {
	state := r.current.Load()
	if state == nil {
		return nil, false
	}
	v, ok := state.catalogs[pin]
	return v, ok
}

// Compatible proves only an explicitly approved semantic-equivalent direction.
// It neither changes policy nor substitutes for the server's atomic migration CAS.
func (r *CatalogRegistry) Compatible(from, to CatalogPin) bool {
	state := r.current.Load()
	return state != nil && state.pairs[DirectedCatalogPair{from, to}]
}
