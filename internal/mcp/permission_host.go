package mcp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

// PermissionHostConfig is private operator configuration; it is never a tool
// argument or inherited worker environment. Certificate/worker isolation is
// supplied by the endpoint's authenticated launcher, independently of policy.
type PermissionHostConfig struct {
	Version             int                                `json:"version"`
	Origin              string                             `json:"origin"`
	BotUID              string                             `json:"bot_uid"`
	RecipientID         string                             `json:"recipient_id"`
	EndpointIncarnation string                             `json:"endpoint_incarnation"`
	PeerSHA256          string                             `json:"peer_sha256"`
	ControlAddress      string                             `json:"control_address"`
	RelayAddress        string                             `json:"relay_address"`
	RelayURL            string                             `json:"relay_url"`
	Certificate         string                             `json:"certificate"`
	PrivateKey          string                             `json:"private_key"`
	PeerCA              string                             `json:"peer_ca"`
	ReleasePath         string                             `json:"release_path"`
	ReleaseSHA256       string                             `json:"release_sha256"`
	ReadyPath           string                             `json:"ready_path"`
	RelayExecutable     string                             `json:"relay_executable"`
	AuthorityPins       map[string]permission.AuthorityPin `json:"authority_pins"`
}

type hostIngress struct {
	observation permission.Observation
	admitted    bool
}
type hostCapsule struct {
	id, turnID, generation, contextID, owner, ingressID string
	header                                              permission.NativeHeader
	invocation                                          *permission.Invocation
	bound                                               bool
	relay                                               *hostRelay
	sealed, mac                                         []byte
	manifest                                            []byte
	manifestSize                                        int
	manifestHash                                        string
}
type hostRelay struct {
	token   string
	turn    *ManagedTurn
	capsule *hostCapsule
}

// PermissionHost owns all sessions/turn relays and one process-wide coordinator.
// It has no fallback from a failed managed session to the unmanaged MCP server.
type PermissionHost struct {
	config                    PermissionHostConfig
	coordinator               *permission.Coordinator
	policies                  permission.PolicyReader
	native                    *permission.NativeReader
	incarnation               string
	macKey                    [32]byte
	mu                        sync.Mutex
	sessions                  map[string]bool
	ingress                   map[string]*hostIngress
	capsules                  map[string]*hostCapsule
	relays                    map[string]*hostRelay
	seenDeliveries            map[[32]byte]bool
	server                    func(*ManagedTurn) *Server
	handlers                  sync.WaitGroup
	closing                   bool
	executable, executableSHA string
}

// NewPermissionHost composes delivered readers; neither a fake provider nor a
// catalog index is installed by default. The embedding command validates its
// complete allowlisted release before constructing the coordinator.
func NewPermissionHost(cfg PermissionHostConfig, c *permission.Coordinator, p permission.PolicyReader, n *permission.NativeReader, server func(*ManagedTurn) *Server) (*PermissionHost, error) { //nolint:gocritic,gocyclo // closed fixed-program branches retain explicit checks in security order
	if cfg.Version != 1 || !permission.ValidOrigin(cfg.Origin) || !permission.ValidNativeID(cfg.BotUID) || cfg.RecipientID == "" || len(cfg.RecipientID) > 256 || !controlID(cfg.EndpointIncarnation) || !controlHash(cfg.PeerSHA256) || c == nil || p == nil || n == nil || server == nil {
		return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	for _, ch := range cfg.RecipientID {
		if ch < 33 || ch > 126 {
			return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
	}
	h := &PermissionHost{config: cfg, coordinator: c, policies: p, native: n, incarnation: permission.NewID(), sessions: map[string]bool{}, ingress: map[string]*hostIngress{}, capsules: map[string]*hostCapsule{}, relays: map[string]*hostRelay{}, seenDeliveries: map[[32]byte]bool{}, server: server}
	if _, err := rand.Read(h.macKey[:]); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if cfg.RelayExecutable != "" {
		executable = cfg.RelayExecutable
		err = nil
	}
	if !filepath.IsAbs(executable) {
		return nil, errors.New("RELAY_EXECUTABLE_UNAVAILABLE")
	}
	if err != nil {
		return nil, errors.New("RELAY_EXECUTABLE_UNAVAILABLE")
	}
	f, err := os.Open(executable)
	if err != nil {
		return nil, errors.New("RELAY_EXECUTABLE_UNAVAILABLE")
	}
	defer closePermissionIO(f)
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0o022 != 0 || stat.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("RELAY_EXECUTABLE_UNAVAILABLE")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return nil, errors.New("RELAY_EXECUTABLE_UNAVAILABLE")
	}
	h.executable = executable
	h.executableSHA = hex.EncodeToString(hash.Sum(nil))
	if _, err = TurnRelayHandle(cfg.RelayURL, strings.Repeat("a", 64)); err != nil {
		return nil, err
	}
	return h, nil
}

// Incarnation is a private launcher readiness value, never a worker grant.
func (h *PermissionHost) Incarnation() string { return h.incarnation }

func controlUnavailable(f permission.ControlFrame, reason string, hello bool) map[string]any {
	m := map[string]any{"control_version": 1, "request_id": f.RequestID, "status": "unavailable", "payload": map[string]any{"reason": reason}}
	if hello {
		m["session_id"] = f.SessionID
	}
	return m
}
func controlOK(f permission.ControlFrame, payload any) map[string]any {
	return map[string]any{"control_version": 1, "request_id": f.RequestID, "status": "ok", "payload": payload}
}
func controlError(f permission.ControlFrame, err error) map[string]any {
	var r *permission.Refusal
	if errors.As(err, &r) {
		return map[string]any{"control_version": 1, "request_id": f.RequestID, "status": "refused", "payload": r}
	}
	return controlUnavailable(f, "authority_unavailable", false)
}
func controlString(m map[string]any, key string) string { s, _ := m[key].(string); return s }
func controlID(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == s
}
func controlHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

// ServeControl authenticates the pinned endpoint certificate before parsing a
// private frame. Session/request reuse or malformed frames close without reply.
func (h *PermissionHost) ServeControl(ctx context.Context, conn *tls.Conn) error { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	if err := conn.HandshakeContext(ctx); err != nil {
		return err
	}
	state := conn.ConnectionState()
	if state.Version < tls.VersionTLS13 || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return errors.New("HOST_AUTH_REQUIRED")
	}
	sum := sha256.Sum256(state.PeerCertificates[0].Raw)
	if hex.EncodeToString(sum[:]) != h.config.PeerSHA256 {
		return errors.New("HOST_AUTH_REQUIRED")
	}
	session := ""
	seen := map[string]bool{}
	defer func() { h.closeSession(session); closePermissionIO(conn) }()
	for {
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Minute)); err != nil {
			return err
		}
		var prefix [4]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			return err
		}
		length := binary.BigEndian.Uint32(prefix[:])
		if length == 0 || length > 65536 {
			return errors.New("CONTROL_FRAME_INVALID")
		}
		raw := make([]byte, length)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return err
		}
		f, err := permission.ParseControlFrame(raw)
		if err != nil || seen[f.RequestID] || session != "" && f.SessionID != session {
			return errors.New("CONTROL_FRAME_INVALID")
		}
		seen[f.RequestID] = true
		var reply map[string]any
		if session == "" {
			if f.Method != "hello" {
				return errors.New("CONTROL_HELLO_REQUIRED")
			}
			reply = h.hello(f)
			if reply["status"] == "ok" {
				session = f.SessionID
			}
		} else {
			if f.Method == "hello" {
				return errors.New("CONTROL_HELLO_REPLAY")
			}
			reply = h.handleControl(ctx, session, f)
		}
		encoded, err := permission.PrivateJSON(reply)
		if err != nil {
			return err
		}
		binary.BigEndian.PutUint32(prefix[:], uint32(len(encoded)))
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if _, err = conn.Write(append(prefix[:], encoded...)); err != nil {
			return err
		}
		if session == "" {
			return errors.New("CONTROL_NEGOTIATION_FAILED")
		}
	}
}

func (h *PermissionHost) hello(f permission.ControlFrame) map[string]any {
	p := f.Payload
	if !permission.Fields(p, "versions", "metadata_versions", "identity_versions", "endpoint_incarnation", "configured_origin", "recipient_id") {
		return controlUnavailable(f, "predicate_unsupported", true)
	}
	for _, name := range []string{"versions", "metadata_versions", "identity_versions"} {
		a, ok := p[name].([]any)
		if !ok || len(a) != 1 || a[0] != json.Number("2") {
			return controlUnavailable(f, "predicate_unsupported", true)
		}
	}
	if p["endpoint_incarnation"] != h.config.EndpointIncarnation || p["configured_origin"] != h.config.Origin || p["recipient_id"] != h.config.RecipientID {
		return controlUnavailable(f, "authority_conflict", true)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[f.SessionID] || len(h.sessions) >= 100000 {
		return controlUnavailable(f, "authority_conflict", true)
	}
	h.sessions[f.SessionID] = true
	return controlOK(f, map[string]any{"session_id": f.SessionID, "selected_binding": 2, "selected_metadata": 2, "selected_identity": 2, "coordinator_incarnation": h.incarnation})
}

func (h *PermissionHost) handleControl(ctx context.Context, owner string, f permission.ControlFrame) map[string]any { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	p := f.Payload
	switch f.Method {
	case "register_ingress":
		if !permission.Fields(p, "key", "delivery_reference", "stream", "payload_sha256") {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		keyObject, ok := p["key"].(map[string]any)
		keyBytes, keyErr := permission.PrivateJSON(p["key"])
		var key permission.Key
		if !ok || !permission.Fields(keyObject, "origin", "bot_uid", "space_id") || keyErr != nil || json.Unmarshal(keyBytes, &key) != nil || key.Origin != h.config.Origin || key.BotUID != h.config.BotUID || !key.Valid() {
			return controlUnavailable(f, "authority_conflict", false)
		}
		observed, err := h.native.Resolve(ctx, key, "admit", []string{controlString(p, "delivery_reference")})
		if err != nil {
			return controlError(f, err)
		}
		header := observed[0].Header()
		stream, streamErr := permission.PrivateJSON(p["stream"])
		actual, actualErr := permission.PrivateJSON(header.Delivery)
		if streamErr != nil || actualErr != nil || !bytes.Equal(stream, actual) || p["payload_sha256"] != header.PayloadSHA256 {
			return controlUnavailable(f, "authority_conflict", false)
		}
		identity, identityErr := permission.PrivateJSON([]any{key, header.Delivery, header.MessageID, header.MessageVersion, header.PayloadSHA256})
		if identityErr != nil {
			return controlError(f, identityErr)
		}
		delivery := sha256.Sum256(identity)
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.seenDeliveries[delivery] {
			return controlUnavailable(f, "authority_conflict", false)
		}
		// Handles are reclaimed below; replay tombstones are not. Until a real
		// native no-redelivery fence exists, retain them and stop at the bound.
		if len(h.seenDeliveries) >= 100000 {
			return controlUnavailable(f, "budget_exhausted", false)
		}
		h.seenDeliveries[delivery] = true
		id := permission.NewID()
		h.ingress[owner+":"+id] = &hostIngress{observation: observed[0]}
		return controlOK(f, map[string]any{"ingress_id": id})
	case "admit":
		if !permission.Fields(p, "ingress_id", "proposed_context_id") || !controlID(controlString(p, "proposed_context_id")) {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		h.mu.Lock()
		ingress := h.ingress[owner+":"+controlString(p, "ingress_id")]
		if ingress == nil || ingress.admitted || len(h.capsules) >= 64 {
			h.mu.Unlock()
			return controlUnavailable(f, "authority_conflict", false)
		}
		ingress.admitted = true
		delete(h.ingress, owner+":"+controlString(p, "ingress_id"))
		h.mu.Unlock()
		header := ingress.observation.Header()
		key := permission.Key{Origin: h.config.Origin, BotUID: h.config.BotUID, SpaceID: header.SpaceID}
		// F is acquired by Resolve before this initial P. Register/Admit repeats
		// the complete fresh F/B sequence before any protected host work.
		raw, err := h.policies.ReadBundle(ctx, key)
		if err != nil {
			return controlError(f, err)
		}
		bundle, err := h.coordinator.ReadPolicy(raw)
		if err != nil {
			return controlError(f, err)
		}
		generation := permission.NewID()
		if err = h.native.BindGeneration(generation, ingress.observation, nil); err != nil {
			return controlError(f, err)
		}
		subject := permission.Subject{Caller: header.AuthorUID, Kind: header.Source.Kind, Source: header.Source.ID}
		if header.Parent != nil {
			subject.Parent = *header.Parent
		}
		inv, err := h.coordinator.Register(permission.Binding{Key: key, Incarnation: bundle.Incarnation, ContextEpoch: bundle.ContextEpoch, Generation: generation, Subject: subject})
		if err == nil {
			err = h.coordinator.Admit(ctx, inv)
		}
		if err != nil {
			if inv != nil {
				inv.Close()
			}
			h.native.ForgetGeneration(generation)
			return controlError(f, err)
		}
		capsule := &hostCapsule{id: permission.NewID(), turnID: permission.NewID(), generation: generation, contextID: controlString(p, "proposed_context_id"), owner: owner, ingressID: controlString(p, "ingress_id"), header: header, invocation: inv}
		lease, hard := inv.Deadlines()
		now := hard.Add(-30 * time.Minute)
		capsule.sealed, err = permission.PrivateJSON(map[string]any{"binding_version": 2, "capsule_id": capsule.id, "turn_id": capsule.turnID, "coordinator_incarnation": h.incarnation, "endpoint_incarnation": h.config.EndpointIncarnation, "recipient_id": h.config.RecipientID, "key": key, "scope_incarnation": bundle.Incarnation, "context_epoch": bundle.ContextEpoch, "author_uid": header.AuthorUID, "author_kind": header.AuthorKind, "Header": header, "context_id": capsule.contextID, "context_generation": generation, "created_at": now.UTC().Format(time.RFC3339Nano), "lease_expires_at": lease.UTC().Format(time.RFC3339Nano), "hard_deadline": hard.UTC().Format(time.RFC3339Nano)})
		if err != nil {
			inv.Close()
			h.native.ForgetGeneration(generation)
			return controlError(f, err)
		}
		mac := hmac.New(sha256.New, h.macKey[:])
		mac.Write(capsule.sealed)
		capsule.mac = mac.Sum(nil)
		h.mu.Lock()
		h.capsules[capsule.id] = capsule
		h.mu.Unlock()
		return controlOK(f, map[string]any{"capsule_id": capsule.id, "turn_id": capsule.turnID, "generation": generation})
	case "bind_context":
		if !permission.Fields(p, "capsule_id", "manifest") {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		c := h.capsule(owner, controlString(p, "capsule_id"))
		if c == nil {
			return controlUnavailable(f, "authority_conflict", false)
		}
		manifest, pending, err := h.contextManifest(c, p)
		if pending && err == nil {
			return controlOK(f, map[string]any{"accepted_bytes": len(manifest)})
		}
		if err != nil {
			return controlError(f, err)
		}
		obj, err := strictObject(manifest, "context_id", "context_generation", "originals")
		if err != nil {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		var contextID, generation string
		if json.Unmarshal(obj["context_id"], &contextID) != nil || json.Unmarshal(obj["context_generation"], &generation) != nil || contextID != c.contextID || generation != c.generation {
			return controlUnavailable(f, "authority_conflict", false)
		}
		var originals []map[string]any
		if json.Unmarshal(obj["originals"], &originals) != nil || len(originals) < 1 || len(originals) > 256 {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		// Decode through the strict canonical decoder to retain integer lexemes.
		core, err := permission.DecodeManifest(manifest)
		if err != nil {
			return controlError(f, err)
		}
		values, ok := core["originals"].([]any)
		if !ok {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		observed := make([]permission.Observation, 0, len(values))
		originalIncluded := false
		expected, err := permission.PrivateJSON(c.header)
		if err != nil {
			return controlError(f, err)
		}
		key := permission.Key{Origin: h.config.Origin, BotUID: h.config.BotUID, SpaceID: c.header.SpaceID}
		for _, value := range values {
			o, err := h.native.ObservedHeader(key, value)
			if err != nil {
				return controlError(f, err)
			}
			raw, err := permission.PrivateJSON(value)
			if err != nil {
				return controlError(f, err)
			}
			originalIncluded = originalIncluded || bytes.Equal(raw, expected)
			observed = append(observed, o)
		}
		if !originalIncluded {
			return controlUnavailable(f, "authority_conflict", false)
		}
		if err = h.native.AttachContributors(c.generation, observed); err == nil {
			err = h.coordinator.Admit(ctx, c.invocation)
		}
		if err != nil {
			h.closeCapsule(c)
			return controlError(f, err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.capsules[c.id] != c {
			return controlUnavailable(f, "authority_conflict", false)
		}
		c.bound = true
		return controlOK(f, map[string]any{"context_generation": c.generation})
	case "open_relay":
		if !permission.Fields(p, "capsule_id", "recipient_id", "transport", "relay_nonce") || !controlID(controlString(p, "relay_nonce")) {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		c := h.capsule(owner, controlString(p, "capsule_id"))
		transport := controlString(p, "transport")
		if c == nil || p["recipient_id"] != h.config.RecipientID || (transport != "http" && transport != "stdio") {
			return controlUnavailable(f, "authority_conflict", false)
		}
		if err := h.coordinator.Admit(ctx, c.invocation); err != nil {
			return controlError(f, err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.capsules[c.id] != c || !c.bound || c.relay != nil {
			return controlUnavailable(f, "authority_conflict", false)
		}
		source := permission.DeclarationV2{Sender: c.header.AuthorUID, Channel: c.header.Source.ID, SpaceID: c.header.SpaceID, MessageID: c.header.MessageID, ChannelType: 1}
		if c.header.Source.Kind == "group" {
			source.ChannelType = 2
		}
		if c.header.Source.Kind == "thread" {
			source.ChannelType = 5
		}
		relay := &hostRelay{token: permission.NewID() + permission.NewID(), turn: NewManagedTurn(h.coordinator, c.invocation, source), capsule: c}
		c.relay = relay
		h.relays[relay.token] = relay
		if transport == "http" {
			return controlOK(f, map[string]any{"transport": "http", "url": h.config.RelayURL, "handle": relay.token})
		}
		handle, err := TurnRelayHandle(h.config.RelayURL, relay.token)
		if err != nil {
			delete(h.relays, relay.token)
			c.relay = nil
			return controlError(f, err)
		}
		return controlOK(f, map[string]any{"transport": "stdio", "executable": h.executable, "executableSha256": h.executableSHA, "handle": handle})
	case "renew":
		if !permission.Fields(p, "capsule_id", "generation", "ingress_id") {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		c := h.capsule(owner, controlString(p, "capsule_id"))
		if c == nil || p["generation"] != c.generation || p["ingress_id"] != c.ingressID {
			return controlUnavailable(f, "authority_conflict", false)
		}
		if err := h.coordinator.Renew(ctx, c.invocation, c.generation); err != nil {
			return controlError(f, err)
		}
		expires, _ := c.invocation.Deadlines()
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.capsules[c.id] != c {
			return controlUnavailable(f, "authority_conflict", false)
		}
		var record map[string]any
		if json.Unmarshal(c.sealed, &record) != nil {
			return controlUnavailable(f, "authority_conflict", false)
		}
		record["lease_expires_at"] = expires.UTC().Format(time.RFC3339Nano)
		sealed, err := permission.PrivateJSON(record)
		if err != nil {
			return controlError(f, err)
		}
		c.sealed = sealed
		mac := hmac.New(sha256.New, h.macKey[:])
		mac.Write(c.sealed)
		c.mac = mac.Sum(nil)
		return controlOK(f, map[string]any{"lease_expires_at": expires.UTC().Format(time.RFC3339Nano)})
	case "close":
		if !permission.Fields(p, "capsule_id", "reason") {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		reason := controlString(p, "reason")
		if reason != "completed" && reason != "cancelled" && reason != "refused" && reason != "expired" && reason != "worker_failed" {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		c := h.capsule(owner, controlString(p, "capsule_id"))
		if c == nil {
			return controlUnavailable(f, "authority_conflict", false)
		}
		h.closeCapsule(c)
		return controlOK(f, map[string]any{})
	case "checkpoint":
		// The actual native FenceThroughV1 producer is external. No fabricated
		// acknowledgement or journal compaction is performed by this host.
		return controlUnavailable(f, "receipt_unavailable", false)
	default:
		return controlUnavailable(f, "predicate_unsupported", false)
	}
}

func (h *PermissionHost) capsule(owner, id string) *hostCapsule {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.capsules[id]
	if c == nil || c.owner != owner {
		return nil
	}
	mac := hmac.New(sha256.New, h.macKey[:])
	mac.Write(c.sealed)
	if !hmac.Equal(mac.Sum(nil), c.mac) {
		return nil
	}
	return c
}
func (h *PermissionHost) closeCapsule(c *hostCapsule) {
	h.mu.Lock()
	delete(h.capsules, c.id)
	clear(c.manifest)
	c.manifest = nil
	if c.relay != nil {
		delete(h.relays, c.relay.token)
	}
	h.mu.Unlock()
	if c.relay != nil {
		c.relay.turn.Close()
	} else {
		c.invocation.Close()
	}
	h.native.ForgetGeneration(c.generation)
}
func (h *PermissionHost) closeSession(session string) {
	h.mu.Lock()
	for key := range h.ingress {
		if strings.HasPrefix(key, session+":") {
			delete(h.ingress, key)
		}
	}
	var all []*hostCapsule
	for _, c := range h.capsules {
		if c.owner == session {
			all = append(all, c)
		}
	}
	h.mu.Unlock()
	for _, c := range all {
		h.closeCapsule(c)
	}
}

// contextManifest stages only inert private metadata. Chunk offsets, size and
// digest are fixed by the first descriptor; no paths, URLs or content bodies
// are accepted. This preserves the 1 MiB manifest domain over 64 KiB frames.
func (h *PermissionHost) contextManifest(c *hostCapsule, p map[string]any) (dataBytes []byte, pending bool, failure error) { //nolint:gocyclo // one immutable descriptor is assembled before any context binding
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.capsules[c.id] != c || c.bound {
		return nil, false, permission.ContextUntrusted()
	}
	m, ok := p["manifest"].(map[string]any)
	if !ok {
		return nil, false, permission.ContextUntrusted()
	}
	number := func(key string) (int64, error) {
		n, ok := m[key].(json.Number)
		if !ok {
			return 0, errors.New("invalid descriptor")
		}
		return n.Int64()
	}
	total, totalErr := number("bytes")
	hash := controlString(m, "sha256")
	if totalErr != nil || total < 1 || total > 1<<20 || !controlHash(hash) {
		return nil, false, permission.ContextUntrusted()
	}
	if _, chunk := m["carrier"]; !chunk {
		if !permission.Fields(m, "context_id", "context_generation", "originals", "bytes", "sha256") || c.manifestSize != 0 {
			return nil, false, permission.ContextUntrusted()
		}
		core := map[string]any{"context_id": m["context_id"], "context_generation": m["context_generation"], "originals": m["originals"]}
		b, err := permission.PrivateJSON(core)
		if err != nil || int64(len(b)) != total || sha256Sum(b) != hash {
			return nil, false, permission.ContextUntrusted()
		}
		return b, false, nil
	}
	if !permission.Fields(m, "carrier", "bytes", "sha256", "offset", "data_base64") || m["carrier"] != "canonical-json-chunks-v1" {
		return nil, false, permission.ContextUntrusted()
	}
	offset, err := number("offset")
	data, decodeErr := base64.StdEncoding.Strict().DecodeString(controlString(m, "data_base64"))
	if err != nil || decodeErr != nil || offset < 0 || offset > total || len(data) == 0 || int64(len(data)) > total-offset {
		return nil, false, permission.ContextUntrusted()
	}
	if c.manifestSize == 0 {
		c.manifestSize = int(total)
		c.manifestHash = hash
	}
	if int64(c.manifestSize) != total || c.manifestHash != hash || offset != int64(len(c.manifest)) {
		return nil, false, permission.ContextUntrusted()
	}
	c.manifest = append(c.manifest, data...)
	if len(c.manifest) < c.manifestSize {
		return c.manifest, true, nil
	}
	if sha256Sum(c.manifest) != c.manifestHash {
		return nil, false, permission.ContextUntrusted()
	}
	canonical, err := permission.CanonicalManifest(c.manifest)
	if err != nil || !bytes.Equal(canonical, c.manifest) {
		return nil, false, permission.ContextUntrusted()
	}
	return append([]byte(nil), c.manifest...), false, nil
}

// ServeHTTP is only the turn-limited data relay. Bot bearers and host-control
// messages are not accepted here, and relay credentials never reach Bot APIs.
func (h *PermissionHost) ServeHTTP(w http.ResponseWriter, r *http.Request) { //nolint:gocyclo // authenticate the fixed turn route before parsing or injecting declarations
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		http.Error(w, "relay unavailable", http.StatusServiceUnavailable)
		return
	}
	h.handlers.Add(1)
	h.mu.Unlock()
	defer h.handlers.Done()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	for name := range r.Header {
		if strings.EqualFold(name, headerSpaceID) || strings.EqualFold(name, headerChannelID) || strings.EqualFold(name, headerChannelType) || strings.EqualFold(name, headerOnBehalfOf) {
			http.Error(w, "context headers forbidden", http.StatusForbidden)
			return
		}
	}
	h.mu.Lock()
	relay := h.relays[bearerToken(r)]
	h.mu.Unlock()
	if relay == nil {
		http.Error(w, "relay unavailable", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBodyBytes+1))
	if err != nil || len(body) > maxHTTPBodyBytes {
		http.Error(w, "invalid request", 400)
		return
	}
	var req rpcRequest
	if decodeRPCRequest(body, &req) != nil {
		writeHTTPError(w, nil, codeParseError, "PARSE_ERROR")
		return
	}
	if err = relayNegotiation(&req); err == nil {
		err = relayDeclaration(&req, relay.turn.source)
	}
	if err != nil {
		if encodeErr := json.NewEncoder(w).Encode(permissionResponse(req.ID, permission.ContextUntrusted())); encodeErr != nil {
			return
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	server := h.server(relay.turn)
	resp, has, call := server.dispatchManaged(ctx, req)
	w.Header().Set("Content-Type", "application/json")
	if !has {
		w.WriteHeader(202)
		return
	}
	if err := writeManaged(ctx, w, resp, call); err != nil {
		return
	}
}

func relayDeclaration(req *rpcRequest, source permission.DeclarationV2) error {
	if req.Method == "initialize" || req.Method == "ping" || req.Method == "tools/list" || req.isNotification() {
		return nil
	}
	params, err := strictObject(req.Params)
	if err != nil {
		return err
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := params["_meta"]; ok {
		meta, err = strictObject(raw)
		if err != nil {
			return err
		}
	}
	if raw, ok := meta["octo"]; ok {
		claim, err := permission.ParseDeclarationV2(raw)
		if err != nil || claim != source {
			return errors.New("CONTEXT_INVALID")
		}
	}
	meta["octo"], err = json.Marshal(map[string]any{"version": 2, "im": map[string]any{"sender": source.Sender, "channel": source.Channel, "channel_type": source.ChannelType, "space_id": source.SpaceID, "message_id": source.MessageID}})
	if err != nil {
		return err
	}
	params["_meta"], err = json.Marshal(meta)
	if err != nil {
		return err
	}
	req.Params, err = json.Marshal(params)
	return err
}
