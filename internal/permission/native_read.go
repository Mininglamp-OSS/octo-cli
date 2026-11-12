package permission

import (
	"bytes"
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed contracts/shared-contract-v6.json
var nativeSharedContract []byte

// NativeHeader is the unchanged metadata-only native Header wire record.
type NativeHeader struct {
	Version   int    `json:"header_version"`
	Reference string `json:"header_reference"`
	MessageID string `json:"message_id"`
	Source    struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"source"`
	Parent         *string `json:"parent_group"`
	SpaceID        string  `json:"space_id"`
	AuthorUID      string  `json:"author_uid"`
	AuthorKind     string  `json:"author_kind"`
	BotUID         string  `json:"recipient_bot_uid"`
	MessageVersion string  `json:"native_message_version"`
	PayloadSHA256  string  `json:"payload_sha256"`
	Delivery       struct {
		StreamID    string `json:"stream_id"`
		Incarnation string `json:"stream_incarnation"`
		Sequence    string `json:"sequence"`
	} `json:"delivery"`
}

// AuthorityPin identifies a native authority whose currentness/fencing contract
// is configured by the trusted host, never learned from an untrusted response.
type AuthorityPin struct {
	Kind        string `json:"kind"`
	Incarnation string `json:"incarnation"`
}

// Observation cannot be minted by a declaration: only an authenticated native
// response constructs its private reader/key/reference provenance.
type Observation struct {
	header    NativeHeader
	raw       []byte
	reader    *NativeReader
	key       Key
	reference string
	eligible  bool
}

// Header returns a detached metadata copy for private host correlation.
func (o *Observation) Header() NativeHeader {
	h := o.header
	if h.Parent != nil {
		parent := *h.Parent
		h.Parent = &parent
	}
	return h
}

type nativeGeneration struct {
	key          Key
	original     Observation
	contributors []Observation
	bound        bool
}

const (
	// MaxNativeReads caps retained read_id and authority read_nonce replay fences.
	// Without a proven native horizon, saturation fails closed rather than evicting fences.
	MaxNativeReads = 100000
	// MaxNativeObservations caps retained cross-read HeaderV1 immutability records.
	MaxNativeObservations = 100000
	// MaxNativeGenerations caps concurrently active generation bindings.
	MaxNativeGenerations = 1024
)

// NativeReader consumes the existing exact-Bot native-facts contract. It does
// not provide the server's native authority, disclosure or checkpoint methods.
type NativeReader struct {
	self            *SelfPolicyReader
	pins            map[string]AuthorityPin
	profiles        map[string][]string
	mu              sync.Mutex
	reads           map[string]bool
	generations     map[string]nativeGeneration
	observations    map[headerKey]Observation
	maxReads        int
	maxObservations int
	maxGenerations  int
}
type headerKey struct {
	Key
	reference string
}

func (r *NativeReader) readLimit() int {
	if r.maxReads > 0 {
		return r.maxReads
	}
	return MaxNativeReads
}

func (r *NativeReader) observationLimit() int {
	if r.maxObservations > 0 {
		return r.maxObservations
	}
	return MaxNativeObservations
}

func (r *NativeReader) generationLimit() int {
	if r.maxGenerations > 0 {
		return r.maxGenerations
	}
	return MaxNativeGenerations
}

// NewNativeReader fixes authority pins and the exact unchanged predicate table.
func NewNativeReader(self *SelfPolicyReader, pins map[string]AuthorityPin) (*NativeReader, error) {
	if self == nil || len(pins) == 0 {
		return nil, ErrUnavailable
	}
	copyPins := map[string]AuthorityPin{}
	for id, p := range pins {
		if !asciiToken(id, 64, false) || (p.Kind != "sql" && p.Kind != "im") || !validHex(p.Incarnation, 32) {
			return nil, ErrInvalid
		}
		copyPins[id] = p
	}
	var shared struct {
		Profiles map[string][]string `json:"predicate_profiles"`
	}
	if json.Unmarshal(nativeSharedContract, &shared) != nil || len(shared.Profiles) != 10 {
		return nil, ErrUnavailable
	}
	return &NativeReader{self: self, pins: copyPins, profiles: shared.Profiles, reads: map[string]bool{}, generations: map[string]nativeGeneration{}, observations: map[headerKey]Observation{}}, nil
}

func token(s string) bool {
	if s == "" || len(s) > 512 {
		return false
	}
	for _, c := range s {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !valid {
			return false
		}
	}
	return true
}
func utcTimestamp(v any) bool {
	s, ok := v.(string)
	if !ok || !strings.HasSuffix(s, "Z") {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}

func parseNativeHeader(v any) (NativeHeader, []byte, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	m := object(v)
	if !fields(m, "header_version", "header_reference", "message_id", "source", "parent_group", "space_id", "author_uid", "author_kind", "recipient_bot_uid", "native_message_version", "payload_sha256", "delivery") || m["header_version"] != json.Number("1") {
		return NativeHeader{}, nil, ErrUnavailable
	}
	if !token(text(m["header_reference"])) || !token(text(m["native_message_version"])) || !validHex(text(m["payload_sha256"]), 64) {
		return NativeHeader{}, nil, ErrUnavailable
	}
	var h NativeHeader
	raw := canonical(m)
	if json.Unmarshal(raw, &h) != nil {
		return h, nil, ErrUnavailable
	}
	channelType := 1
	switch h.Source.Kind {
	case "dm":
	case "group":
		channelType = 2
	case "thread":
		channelType = 5
	default:
		return h, nil, ErrUnavailable
	}
	decl, err := json.Marshal(map[string]any{"version": 2, "im": map[string]any{"sender": h.AuthorUID, "channel": h.Source.ID, "channel_type": channelType, "space_id": h.SpaceID, "message_id": h.MessageID}})
	if err != nil {
		return h, nil, ErrUnavailable
	}
	if _, err := ParseDeclarationV2(decl); err != nil || !ValidNativeID(h.BotUID) {
		return h, nil, ErrUnavailable
	}
	if !fields(object(m["source"]), "kind", "id") || !fields(object(m["delivery"]), "stream_id", "stream_incarnation", "sequence") || !asciiToken(h.Delivery.StreamID, 128, false) || !validHex(h.Delivery.Incarnation, 32) {
		return h, nil, ErrUnavailable
	}
	if _, err := U64(h.Delivery.Sequence); err != nil {
		return h, nil, ErrUnavailable
	}
	if h.Source.Kind == "thread" {
		if h.Parent == nil || !ValidNativeID(*h.Parent) {
			return h, nil, ErrUnavailable
		}
	} else if h.Parent != nil {
		return h, nil, ErrUnavailable
	}
	if h.AuthorKind != "human" && h.AuthorKind != "user_bot" && h.AuthorKind != "app_bot" && h.AuthorKind != "system" {
		return h, nil, ErrUnavailable
	}
	return h, raw, nil
}

// Resolve performs one fixed authenticated metadata request; no body/history
// retrieval or implicit lookup by a supplied author/conversation is possible.
func (r *NativeReader) Resolve(ctx context.Context, key Key, purpose string, references []string) ([]Observation, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	if key.Origin != r.self.origin || key.BotUID != r.self.botUID || !key.Valid() || len(references) < 1 || len(references) > 32 || (purpose != "admit" && purpose != "contributor" && purpose != "reply") {
		return nil, ErrUnavailable
	}
	refs := make([]map[string]string, len(references))
	seen := map[string]bool{}
	for i, ref := range references {
		if !token(ref) || seen[ref] {
			return nil, ErrUnavailable
		}
		seen[ref] = true
		refs[i] = map[string]string{"delivery_reference": ref}
	}
	id := randomID()
	body, err := json.Marshal(map[string]any{"schema_version": 1, "profile_version": 1, "request_id": id, "operation": "resolve", "purpose": purpose, "references": refs})
	if err != nil || len(body) > 32768 {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, key.Origin+"/v1/bot/permission-facts/v1?"+url.Values{"space_id": {key.SpaceID}}.Encode(), bytes.NewReader(body))
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+r.self.token)
	req.Header.Set("X-Space-ID", key.SpaceID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-store")
	resp, err := r.self.client.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer closeOwned(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" {
		return nil, ErrUnavailable
	}
	v, err := decodeStrict(io.LimitReader(resp.Body, MaxBundleBytes+1), MaxBundleBytes)
	if err != nil {
		return nil, ErrUnavailable
	}
	m := object(v)
	if !fields(m, "schema_version", "profile_version", "key", "request_id", "read_id", "observed_at", "authorities", "results") || m["schema_version"] != json.Number("1") || m["profile_version"] != json.Number("1") || m["request_id"] != id || !validHex(text(m["read_id"]), 32) || !utcTimestamp(m["observed_at"]) {
		return nil, ErrUnavailable
	}
	var actual Key
	if json.Unmarshal(canonical(m["key"]), &actual) != nil || actual != key || !fields(object(m["key"]), "origin", "bot_uid", "space_id") {
		return nil, ErrUnavailable
	}
	authorities, ok := items(m["authorities"], len(r.pins), len(r.pins))
	if !ok {
		return nil, ErrUnavailable
	}
	nonces := map[string]bool{}
	last := ""
	for _, v := range authorities {
		a := object(v)
		id := text(a["authority_id"])
		pin, ok := r.pins[id]
		if !ok || id <= last || !fields(a, "authority_id", "kind", "incarnation", "position", "read_nonce", "consistency", "started_at", "completed_at") || a["kind"] != pin.Kind || a["incarnation"] != pin.Incarnation || a["consistency"] != "ack_before_read_start" || !token(text(a["position"])) || !validHex(text(a["read_nonce"]), 32) || !utcTimestamp(a["started_at"]) || !utcTimestamp(a["completed_at"]) {
			return nil, ErrUnavailable
		}
		last = id
		nonce := text(a["read_nonce"])
		if nonces[nonce] {
			return nil, ErrUnavailable
		}
		nonces[nonce] = true
	}
	results, ok := items(m["results"], len(references), len(references))
	if !ok {
		return nil, ErrUnavailable
	}
	observations := make([]Observation, len(results))
	for i, v := range results {
		a := object(v)
		if len(canonical(a)) > 8192 || a["reference"] != references[i] || !fields(a, "reference", "status", "header", "profile", "authority_refs", "predicates") || a["status"] != "resolved" {
			return nil, ErrUnavailable
		}
		h, raw, err := parseNativeHeader(a["header"])
		if err != nil || h.BotUID != key.BotUID || h.SpaceID != key.SpaceID {
			return nil, ErrUnavailable
		}
		profile := h.Source.Kind + "_" + purpose + "_v1"
		if h.AuthorKind != "human" {
			profile = "unsupported_principal_v1"
		}
		if a["profile"] != profile {
			return nil, ErrUnavailable
		}
		atoms := r.profiles[profile]
		predicates := object(a["predicates"])
		if len(atoms) == 0 || !fields(predicates, atoms...) {
			return nil, ErrUnavailable
		}
		eligible := true
		for _, atom := range atoms {
			value, ok := predicates[atom].(bool)
			if !ok {
				return nil, ErrUnavailable
			}
			eligible = eligible && value
		}
		if profile == "unsupported_principal_v1" && (predicates["identity_supported"] != false || predicates["author_is_human"] != false) {
			return nil, ErrUnavailable
		}
		used, ok := items(a["authority_refs"], 1, len(nonces))
		if !ok {
			return nil, ErrUnavailable
		}
		unique := map[string]bool{}
		for _, v := range used {
			n := text(v)
			if !nonces[n] || unique[n] {
				return nil, ErrUnavailable
			}
			unique[n] = true
		}
		observations[i] = Observation{header: h, raw: raw, reader: r, key: key, reference: references[i], eligible: eligible}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	readID := "read:" + text(m["read_id"])
	if r.reads[readID] {
		return nil, ErrUnavailable
	}
	for n := range nonces {
		if r.reads[n] {
			return nil, ErrUnavailable
		}
	}
	newObservations := map[headerKey]bool{}
	for j := range observations {
		o := &observations[j]
		k := headerKey{o.key, o.header.Reference}
		if prior, ok := r.observations[k]; ok {
			if !bytes.Equal(prior.raw, o.raw) {
				return nil, ErrUnavailable
			}
		} else {
			newObservations[k] = true
		}
	}
	if len(r.reads)+1+len(nonces) > r.readLimit() || len(r.observations)+len(newObservations) > r.observationLimit() {
		return nil, ErrUnavailable
	}
	for n := range nonces {
		r.reads[n] = true
	}
	r.reads[readID] = true
	for j := range observations {
		o := &observations[j]
		k := headerKey{o.key, o.header.Reference}
		r.observations[k] = *o
	}
	return observations, nil
}

// ConfigureBounds overrides the fail-closed retention ceilings before any reads
// or generations are recorded. It never enables sliding-window or TTL eviction.
func (r *NativeReader) ConfigureBounds(maxReads, maxObservations, maxGenerations int) error {
	if maxReads < 1 || maxReads > MaxNativeReads || maxObservations < 1 || maxObservations > MaxNativeObservations || maxGenerations < 1 || maxGenerations > MaxNativeGenerations {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reads) != 0 || len(r.observations) != 0 || len(r.generations) != 0 {
		return ErrConflict
	}
	r.maxReads = maxReads
	r.maxObservations = maxObservations
	r.maxGenerations = maxGenerations
	return nil
}

// ObservedHeader joins an endpoint's exact Header to a native-issued delivery
// reference previously resolved by this host. Header and delivery tokens are
// never treated as interchangeable. Unseen historical headers need the actual
// native reference bridge; a declaration cannot manufacture that proof.
func (r *NativeReader) ObservedHeader(key Key, value any) (Observation, error) {
	h, raw, err := parseNativeHeader(value)
	if err != nil {
		return Observation{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.observations[headerKey{key, h.Reference}]
	if !ok || !bytes.Equal(o.raw, raw) {
		return Observation{}, ErrUnavailable
	}
	return o, nil
}

// BindGeneration records the complete host-owned original union before relay
// access. A record from another reader/key cannot supply contributor authority.
func (r *NativeReader) BindGeneration(generation string, original Observation, contributors []Observation) error { //nolint:gocritic // immutable protocol/config snapshots must be detached by value
	if original.reader != r || len(contributors) > 256 || !validHex(generation, 32) {
		return ErrUnavailable
	}
	copyContributors := append([]Observation(nil), contributors...)
	for oi := range copyContributors {
		o := &copyContributors[oi]
		if o.reader != r || o.key != original.key {
			return ErrUnavailable
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.generations[generation]; exists {
		return ErrConflict
	}
	if len(r.generations) >= r.generationLimit() {
		return ErrUnavailable
	}
	r.generations[generation] = nativeGeneration{key: original.key, original: original, contributors: copyContributors}
	return nil
}

// ForgetGeneration erases private handles; it never rewinds native replay fences.
func (r *NativeReader) ForgetGeneration(generation string) {
	r.mu.Lock()
	delete(r.generations, generation)
	r.mu.Unlock()
}

// Recheck resolves original/current facts and every contributing original anew.
func (r *NativeReader) Recheck(ctx context.Context, b Binding, needs Needs) (Facts, error) { //nolint:gocritic // immutable protocol/config snapshots must be detached by value
	r.mu.Lock()
	g, ok := r.generations[b.Generation]
	r.mu.Unlock()
	if !ok || g.key != b.Key {
		return Facts{}, ErrUnavailable
	}
	current, err := r.Resolve(ctx, b.Key, "admit", []string{g.original.reference})
	if err != nil {
		return Facts{}, err
	}
	if !bytes.Equal(current[0].raw, g.original.raw) {
		return Facts{}, ContextUntrusted()
	}
	h := current[0].header
	subject := Subject{Caller: h.AuthorUID, Kind: h.Source.Kind, Source: h.Source.ID}
	if h.Parent != nil {
		subject.Parent = *h.Parent
	}
	f := Facts{Subject: subject, Complete: true, InvocationEligible: current[0].eligible, CapabilityEligible: current[0].eligible}
	if needs.Invocation {
		for start := 0; start < len(g.contributors); start += 32 {
			end := start + 32
			if end > len(g.contributors) {
				end = len(g.contributors)
			}
			refs := make([]string, end-start)
			for j := range refs {
				refs[j] = g.contributors[start+j].reference
			}
			batch, err := r.Resolve(ctx, b.Key, "contributor", refs)
			if err != nil {
				return Facts{}, err
			}
			for j := range batch {
				o := &batch[j]
				if !bytes.Equal(o.raw, g.contributors[start+j].raw) {
					return Facts{}, ContextUntrusted()
				}
				s := Subject{Caller: o.header.AuthorUID, Kind: o.header.Source.Kind, Source: o.header.Source.ID}
				if o.header.Parent != nil {
					s.Parent = *o.header.Parent
				}
				f.Contributors = append(f.Contributors, ContributorFact{s, o.eligible})
			}
		}
	}
	return f, nil
}

// TLSConfiguration returns a detached authority configuration for owned sends.
func (r *NativeReader) TLSConfiguration() *tls.Config {
	return r.self.client.Transport.(*http.Transport).TLSClientConfig.Clone()
}

// Authorities returns the configured identifiers, without learning aliases.
func (r *NativeReader) Authorities() []string {
	ids := make([]string, 0, len(r.pins))
	for id := range r.pins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// AttachContributors seals the complete original union before relay access.
func (r *NativeReader) AttachContributors(generation string, originals []Observation) error {
	if len(originals) > 256 {
		return ErrUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.generations[generation]
	if !ok || g.bound {
		return ErrConflict
	}
	seen := map[string]bool{}
	for oi := range originals {
		o := &originals[oi]
		if o.reader != r || o.key != g.key || seen[o.header.Reference] {
			return ErrUnavailable
		}
		seen[o.header.Reference] = true
	}
	g.contributors = append([]Observation(nil), originals...)
	g.bound = true
	r.generations[generation] = g
	return nil
}
