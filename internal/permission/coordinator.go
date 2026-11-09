package permission

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
)

// Refusal contains only the closed, public permission error payload. It never
// reports an action outcome; an earlier action may already have run.
type Refusal struct {
	Version     int    `json:"version"`
	Category    string `json:"category"`
	Correlation string `json:"correlation"`
}

func (r *Refusal) Error() string { return "PERMISSION_REFUSED" }
func refuse(category string) *Refusal {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("permission correlation entropy unavailable")
	}
	return &Refusal{1, category, hex.EncodeToString(b[:])}
}

// Needs describes the independently active stages, not an upstream verdict.
type Needs struct{ Invocation, Capability bool }

// Facts is an in-process adapter result, NOT a replacement native wire format.
// Complete attests all applicable predicates from the unchanged native-facts
// contract, including original/current visibility and context contributors.
// There is deliberately no implementation that promotes declared metadata,
// raw events, roster membership, mirrors or a server policy verdict to Facts.
type Facts struct {
	Subject            Subject
	Complete           bool
	InvocationEligible bool
	CapabilityEligible bool
}

// PolicyReader supplies a new complete authoritative P, never a cached allow.
type PolicyReader interface {
	ReadBundle(context.Context, Key) ([]byte, error)
}

// FactReader is the missing private native/provider composition seam.
type FactReader interface {
	Recheck(context.Context, Binding, Needs) (Facts, error)
}

// Binding is supplied by a private authenticated host integration after its
// native ingress, generation and contributor checks. These fields are never
// accepted as an MCP argument or exported as a model tool.
type Binding struct {
	Key          Key
	Incarnation  string
	ContextEpoch string
	Generation   string
	Subject      Subject
}

// Catalog is copied into the coordinator. The composition owner must verify
// the complete release and support allowlist before constructing it. An index
// digest is not a supported release. Tests use an explicitly synthetic catalog.
type Catalog struct {
	Version      string
	Digest       string
	Capabilities []string
}

type watermark struct {
	revision, epoch uint64
	digest          string
}
type scope struct {
	sync.Mutex
	seen      bool
	watermark watermark
}
type scopeKey struct {
	Key
	incarnation string
}

// Coordinator owns all accepted revisions for its process. Every connection,
// facade and sink must share this instance; a new per-request instance is not
// a permission enforcement composition.
type Coordinator struct {
	mu       sync.Mutex
	scopes   map[scopeKey]*scope
	policies PolicyReader
	facts    FactReader
	catalog  Catalog
	caps     map[string]bool
}

// Invocation is an opaque, revocable host handle. Handles from an earlier
// coordinator/process cannot be used after restart, even for U/U policies.
type Invocation struct {
	owner   *Coordinator
	binding Binding
	mu      sync.Mutex
	closed  bool
}

// NewCoordinator copies a prevalidated supported catalog and private readers.
func NewCoordinator(catalog Catalog, policies PolicyReader, facts FactReader) (*Coordinator, error) {
	if !asciiToken(catalog.Version, 64, false) || !validHex(catalog.Digest, 64) || policies == nil || facts == nil {
		return nil, ErrUnavailable
	}
	caps := map[string]bool{}
	for _, id := range catalog.Capabilities {
		if !asciiToken(id, 256, false) || caps[id] {
			return nil, ErrInvalid
		}
		caps[id] = true
	}
	if len(caps) == 0 {
		return nil, ErrUnavailable
	}
	catalog.Capabilities = nil
	return &Coordinator{scopes: map[scopeKey]*scope{}, policies: policies, facts: facts, catalog: catalog, caps: caps}, nil
}

// Register is a private host-composition seam, not a control protocol or native
// authentication implementation. Production MCP never calls it today.
func (c *Coordinator) Register(b Binding) (*Invocation, error) { //nolint:gocritic // freeze a private binding by value so host mutation cannot retarget a handle
	if !b.Key.Valid() || !validHex(b.Incarnation, 32) || b.Generation == "" || !ValidNativeID(b.Subject.Caller) ||
		(b.Subject.Kind != "dm" && b.Subject.Kind != "group" && b.Subject.Kind != "thread") || b.Subject.Source == "" {
		return nil, refuse("context_untrusted")
	}
	if _, err := U64(b.ContextEpoch); err != nil {
		return nil, refuse("context_untrusted")
	}
	return &Invocation{owner: c, binding: b}, nil
}

// Close revokes the private handle and joins an owned first write in progress.
func (i *Invocation) Close() { i.mu.Lock(); defer i.mu.Unlock(); i.closed = true }

func (c *Coordinator) scope(b *Binding) *scope {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := scopeKey{b.Key, b.Incarnation}
	s := c.scopes[k]
	if s == nil {
		s = &scope{}
		c.scopes[k] = s
	}
	return s
}

func (s *scope) accept(p *Bundle) error {
	r, err := U64(p.Revision)
	if err != nil {
		return refuse("policy_unavailable")
	}
	e, err := U64(p.ContextEpoch)
	if err != nil {
		return refuse("policy_unavailable")
	}
	if s.seen && (r < s.watermark.revision || e < s.watermark.epoch || r == s.watermark.revision && (p.Digest != s.watermark.digest || e != s.watermark.epoch)) {
		return refuse("policy_unavailable")
	}
	s.seen = true
	s.watermark = watermark{r, e, p.Digest}
	return nil
}

// atBoundary performs F -> newly initiated complete B -> serialized acceptance,
// both decisions from that P, consumption and synchronous first write. It
// returns no reusable allow/lease. Only owned sinks in this package can call it.
func (c *Coordinator) atBoundary(ctx context.Context, i *Invocation, capability string, start func() error) error { //nolint:gocyclo // ordering of refresh, acceptance, both stages and owned write is one indivisible security boundary
	if i == nil || i.owner != c {
		return refuse("context_untrusted")
	}
	i.mu.Lock()
	closed := i.closed
	i.mu.Unlock()
	if closed {
		return refuse("context_untrusted")
	}
	b := i.binding
	need := Needs{}
	for round := 0; round < 3; round++ {
		if err := ctx.Err(); err != nil {
			return refuse("policy_unavailable")
		}
		f, err := c.facts.Recheck(ctx, b, need)
		if err != nil || !f.Complete {
			return refuse("policy_unavailable")
		}
		if f.Subject != b.Subject {
			return refuse("context_untrusted")
		}
		raw, err := c.policies.ReadBundle(ctx, b.Key)
		if err != nil {
			return refuse("policy_unavailable")
		}
		p, err := ParseBundle(bytes.NewReader(raw))
		if err != nil || p.Key != b.Key || p.Incarnation != b.Incarnation || p.CatalogVersion != c.catalog.Version || p.CatalogDigest != c.catalog.Digest {
			return refuse("policy_unavailable")
		}
		// Every supplied capability reference must resolve, including denies.
		for _, rule := range p.Capability.Rules {
			for _, id := range rule["capabilities"].([]any) {
				if !c.caps[id.(string)] {
					return refuse("policy_unavailable")
				}
			}
		}
		s := c.scope(&b)
		if !s.TryLock() {
			// Waiting behind another physical write invalidates this F/B. Join
			// the current owner, then acquire new facts and a new complete P;
			// never dispatch a queued old positive decision after that wait.
			s.Lock()
			cancelled := ctx.Err() != nil
			s.Unlock()
			if cancelled {
				return refuse("policy_unavailable")
			}
			continue
		}
		err = s.accept(&p)
		if err != nil {
			s.Unlock()
			return err
		}
		if p.ContextEpoch != b.ContextEpoch {
			s.Unlock()
			return refuse("context_untrusted")
		}
		next := Needs{p.Invocation.State == "E", p.Capability.State == "E"}
		if next.Invocation && !need.Invocation || next.Capability && !need.Capability {
			s.Unlock()
			need = next
			continue // new F then new B; never reuse this P
		}
		i.mu.Lock()
		if i.closed || p.ContextEpoch != b.ContextEpoch {
			err = refuse("context_untrusted")
		} else if next.Invocation && (!f.InvocationEligible || !stageAllows(p.Invocation, f.Subject, "")) {
			err = refuse("invocation_denied")
		} else if !c.caps[capability] || next.Capability && (!f.CapabilityEligible || !stageAllows(p.Capability, f.Subject, capability)) {
			err = refuse("capability_denied")
		} else if ctx.Err() != nil {
			err = refuse("policy_unavailable")
		} else {
			err = start()
		}
		i.mu.Unlock()
		s.Unlock()
		return err
	}
	return refuse("policy_unavailable")
}

// IsRefusal identifies a typed nonretryable permission refusal.
func IsRefusal(err error) bool { var r *Refusal; return errors.As(err, &r) }
