package mcp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	RelayCA             string                             `json:"relay_ca,omitempty"`
	RelayCASHA256       string                             `json:"relay_ca_sha256,omitempty"`
	RelayPeerSHA256     string                             `json:"relay_peer_sha256,omitempty"`
	RelayCertSHA256     string                             `json:"relay_cert_sha256,omitempty"`
	RelayServerName     string                             `json:"relay_server_name,omitempty"`
	ResponseBudgets     *permission.ResponseBudgets        `json:"response_budgets,omitempty"`
	AuthorityPins       map[string]permission.AuthorityPin `json:"authority_pins"`
}

type hostIngress struct {
	observation permission.Observation
	admitted    bool
}
type hostContextGeneration struct {
	key              permission.Key
	scopeIncarnation string
	contextEpoch     string
	generation       string
}
type hostCapsule struct {
	id, turnID, generation, contextID, owner, ingressID string
	scopeIncarnation, contextEpoch                      string
	key                                                 permission.Key
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
	contextGenerations        map[string]hostContextGeneration
	relays                    map[string]*hostRelay
	seenDeliveries            map[[32]byte]bool
	server                    func(*ManagedTurn) *Server
	handlers                  sync.WaitGroup
	closing                   bool
	executable, executableSHA string
	relayTrust                TurnRelayTrust
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
	var caPEM, caSHA256 string
	if cfg.RelayCA != "" {
		if !filepath.IsAbs(cfg.RelayCA) || strings.ContainsAny(cfg.RelayCA, "\r\n\x00") {
			return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
		pemBytes, err := os.ReadFile(cfg.RelayCA)
		if err != nil || len(pemBytes) == 0 || len(pemBytes) > 256*1024 || !x509.NewCertPool().AppendCertsFromPEM(pemBytes) {
			return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
		caPEM = string(pemBytes)
		caSum := sha256.Sum256(pemBytes)
		caSHA256 = hex.EncodeToString(caSum[:])
	}
	if cfg.RelayCASHA256 != "" && (!controlHash(cfg.RelayCASHA256) || (caSHA256 != "" && cfg.RelayCASHA256 != caSHA256)) {
		return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if cfg.RelayPeerSHA256 != "" && !controlHash(cfg.RelayPeerSHA256) {
		return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if cfg.RelayCertSHA256 != "" && !controlHash(cfg.RelayCertSHA256) {
		return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if cfg.RelayPeerSHA256 != "" && cfg.RelayCertSHA256 != "" && cfg.RelayPeerSHA256 != cfg.RelayCertSHA256 {
		return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	peerSHA256 := cfg.RelayPeerSHA256
	if peerSHA256 == "" {
		peerSHA256 = cfg.RelayCertSHA256
	}
	certSHA256 := cfg.RelayCertSHA256
	if certSHA256 == "" {
		certSHA256 = cfg.RelayPeerSHA256
	}
	effectiveCASHA256 := cfg.RelayCASHA256
	if effectiveCASHA256 == "" && peerSHA256 != "" && caSHA256 != "" {
		effectiveCASHA256 = caSHA256
	}
	if (effectiveCASHA256 != "") != (peerSHA256 != "") && (cfg.RelayCASHA256 != "" || cfg.RelayPeerSHA256 != "") {
		return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if cfg.RelayServerName != "" && !validRelayServerName(cfg.RelayServerName) {
		return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if cfg.ResponseBudgets != nil {
		if !cfg.ResponseBudgets.Valid() {
			return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
		if err := c.ConfigureResponseBudgets(*cfg.ResponseBudgets); err != nil {
			return nil, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
		permission.RegisterRelayEndpointBudgets(cfg.RelayURL, *cfg.ResponseBudgets)
	}
	trust := TurnRelayTrust{CAFile: cfg.RelayCA, CAPEM: caPEM, CASHA256: effectiveCASHA256, PeerSHA256: peerSHA256, CertSHA256: certSHA256, ServerName: cfg.RelayServerName}
	h := &PermissionHost{config: cfg, coordinator: c, policies: p, native: n, incarnation: permission.NewID(), sessions: map[string]bool{}, ingress: map[string]*hostIngress{}, capsules: map[string]*hostCapsule{}, contextGenerations: map[string]hostContextGeneration{}, relays: map[string]*hostRelay{}, seenDeliveries: map[[32]byte]bool{}, server: server, relayTrust: trust}
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
	if _, err = TurnRelayHandleWithTrust(cfg.RelayURL, strings.Repeat("a", 64), h.relayTrust); err != nil {
		return nil, err
	}
	if _, err = TurnRelayHandleWithTrust(cfg.RelayURL, strings.Repeat("a", 64), TurnRelayTrust{CAFile: h.relayTrust.CAFile, CertSHA256: h.relayTrust.CertSHA256, ServerName: h.relayTrust.ServerName}); err != nil {
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
	// Sessions are replay tombstones, not active-connection accounting.
	// Hello semantics require retention after disconnect and fail-closed
	// saturation; teardown cannot evict them to admit reused sessions.
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
		if (!permission.Fields(p, "ingress_id", "proposed_context_id") && !permission.Fields(p, "ingress_id", "proposed_context_id", "prior_generation") && !permission.Fields(p, "ingress_id", "proposed_context_id", "context_generation")) || !controlID(controlString(p, "proposed_context_id")) {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		requestedGen := controlString(p, "prior_generation")
		if _, hasPriorGen := p["prior_generation"]; hasPriorGen && !controlID(requestedGen) {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		if requestedGen == "" {
			requestedGen = controlString(p, "context_generation")
			if _, hasContextGen := p["context_generation"]; hasContextGen && !controlID(requestedGen) {
				return controlUnavailable(f, "predicate_unsupported", false)
			}
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
		proposedContextID := controlString(p, "proposed_context_id")
		generation := permission.NewID()
		h.mu.Lock()
		prior, hasPrior := h.contextGenerations[owner+":"+proposedContextID]
		activeConflict := false
		for _, existing := range h.capsules {
			if existing.owner == owner && existing.contextID == proposedContextID {
				activeConflict = true
				break
			}
		}
		if hasPrior && !activeConflict && prior.key == key && prior.scopeIncarnation == bundle.Incarnation && prior.contextEpoch == bundle.ContextEpoch && (requestedGen == "" || requestedGen == prior.generation) {
			generation = prior.generation
		} else if hasPrior {
			delete(h.contextGenerations, owner+":"+proposedContextID)
		}
		h.mu.Unlock()
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
		capsule := &hostCapsule{id: permission.NewID(), turnID: permission.NewID(), generation: generation, contextID: proposedContextID, owner: owner, ingressID: controlString(p, "ingress_id"), scopeIncarnation: bundle.Incarnation, contextEpoch: bundle.ContextEpoch, key: key, header: header, invocation: inv}
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
		return controlOK(f, map[string]any{"capsule_id": capsule.id, "turn_id": capsule.turnID, "generation": generation, "scope_incarnation": bundle.Incarnation, "context_epoch": bundle.ContextEpoch})
	case "resolve_contributors":
		rawRefs, hasDeliveryRefs := p["delivery_references"]
		refField := "delivery_references"
		if !hasDeliveryRefs {
			rawRefs, hasDeliveryRefs = p["references"]
			refField = "references"
		}
		if !hasDeliveryRefs {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		hasCapsule := permission.Fields(p, "capsule_id", refField)
		hasKey := permission.Fields(p, "key", refField)
		hasBoth := permission.Fields(p, "capsule_id", "key", refField)
		if !hasCapsule && !hasKey && !hasBoth {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		var key permission.Key
		if hasCapsule || hasBoth {
			c := h.capsule(owner, controlString(p, "capsule_id"))
			if c == nil {
				return controlUnavailable(f, "authority_conflict", false)
			}
			key = permission.Key{Origin: h.config.Origin, BotUID: h.config.BotUID, SpaceID: c.header.SpaceID}
		}
		if hasKey || hasBoth {
			keyObject, ok := p["key"].(map[string]any)
			keyBytes, keyErr := permission.PrivateJSON(p["key"])
			var parsedKey permission.Key
			if !ok || !permission.Fields(keyObject, "origin", "bot_uid", "space_id") || keyErr != nil || json.Unmarshal(keyBytes, &parsedKey) != nil || parsedKey.Origin != h.config.Origin || parsedKey.BotUID != h.config.BotUID || !parsedKey.Valid() || (hasBoth && parsedKey != key) {
				return controlUnavailable(f, "authority_conflict", false)
			}
			key = parsedKey
		}
		refs, ok := parseContributorReferences(rawRefs, 1, 256)
		if !ok {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		headers, err := h.resolveContributorBatches(ctx, key, refs)
		if err != nil {
			return controlError(f, err)
		}
		return controlOK(f, map[string]any{"headers": headers})
	case "bind_context":
		hasExact := permission.Fields(p, "capsule_id", "manifest")
		hasContribRefs := permission.Fields(p, "capsule_id", "manifest", "contributor_references")
		hasDeliveryRefs := permission.Fields(p, "capsule_id", "manifest", "delivery_references")
		if !hasExact && !hasContribRefs && !hasDeliveryRefs {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		var contributorRefs []string
		if hasContribRefs || hasDeliveryRefs {
			rawRefs := p["contributor_references"]
			if hasDeliveryRefs {
				rawRefs = p["delivery_references"]
			}
			var ok bool
			contributorRefs, ok = parseContributorReferences(rawRefs, 0, 256)
			if !ok {
				return controlUnavailable(f, "predicate_unsupported", false)
			}
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
		if len(contributorRefs) > 0 {
			if _, err := h.resolveContributorBatches(ctx, key, contributorRefs); err != nil {
				h.closeCapsule(c)
				return controlError(f, err)
			}
		}
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
		hasFour := permission.Fields(p, "capsule_id", "recipient_id", "transport", "relay_nonce")
		hasFive := permission.Fields(p, "capsule_id", "recipient_id", "transport", "relay_nonce", "include_tls_trust")
		if (!hasFour && !hasFive) || !controlID(controlString(p, "relay_nonce")) {
			return controlUnavailable(f, "predicate_unsupported", false)
		}
		includeTLS := false
		if hasFive {
			var ok bool
			includeTLS, ok = p["include_tls_trust"].(bool)
			if !ok {
				return controlUnavailable(f, "predicate_unsupported", false)
			}
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
			reply := map[string]any{"transport": "http", "url": h.config.RelayURL, "handle": relay.token}
			if h.relayTrust.CASHA256 != "" && h.relayTrust.PeerSHA256 != "" {
				reply["ca_sha256"] = h.relayTrust.CASHA256
				reply["peer_sha256"] = h.relayTrust.PeerSHA256
			}
			if includeTLS {
				if tlsMap := h.relayTrustMap(); len(tlsMap) > 0 {
					reply["tls"] = tlsMap
				}
			}
			return controlOK(f, reply)
		}
		stdioTrust := h.relayTrust
		if includeTLS {
			stdioTrust = TurnRelayTrust{
				CAFile:     h.relayTrust.CAFile,
				CertSHA256: h.relayTrust.CertSHA256,
				ServerName: h.relayTrust.ServerName,
			}
		}
		handle, err := TurnRelayHandleWithTrust(h.config.RelayURL, relay.token, stdioTrust)
		if err != nil {
			delete(h.relays, relay.token)
			c.relay = nil
			return controlError(f, err)
		}
		reply := map[string]any{"transport": "stdio", "executable": h.executable, "executableSha256": h.executableSHA, "handle": handle}
		if includeTLS {
			if tlsMap := h.relayTrustMap(); len(tlsMap) > 0 {
				reply["tls"] = tlsMap
			}
		}
		return controlOK(f, reply)
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
		h.mu.Lock()
		bound := c.bound
		h.mu.Unlock()
		h.closeCapsule(c)
		if reason == "completed" && bound {
			h.mu.Lock()
			if len(h.contextGenerations) < 10000 {
				h.contextGenerations[owner+":"+c.contextID] = hostContextGeneration{key: c.key, scopeIncarnation: c.scopeIncarnation, contextEpoch: c.contextEpoch, generation: c.generation}
			}
			h.mu.Unlock()
		}
		return controlOK(f, map[string]any{})
	case "checkpoint":
		// The actual native FenceThroughV1 producer is external. No fabricated
		// acknowledgement or journal compaction is performed by this host.
		return controlUnavailable(f, "receipt_unavailable", false)
	default:
		return controlUnavailable(f, "predicate_unsupported", false)
	}
}

func parseContributorReferences(v any, minCount, maxCount int) ([]string, bool) {
	items, ok := v.([]any)
	if !ok || len(items) < minCount || len(items) > maxCount {
		return nil, false
	}
	refs := make([]string, len(items))
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok || s == "" || len(s) > 512 || seen[s] {
			return nil, false
		}
		seen[s] = true
		refs[i] = s
	}
	return refs, true
}

func (h *PermissionHost) resolveContributorBatches(ctx context.Context, key permission.Key, refs []string) ([]permission.NativeHeader, error) {
	headers := make([]permission.NativeHeader, 0, len(refs))
	for start := 0; start < len(refs); start += 32 {
		end := start + 32
		if end > len(refs) {
			end = len(refs)
		}
		batch, err := h.native.Resolve(ctx, key, "contributor", refs[start:end])
		if err != nil {
			return nil, err
		}
		for i := range batch {
			headers = append(headers, batch[i].Header())
		}
	}
	return headers, nil
}

func (h *PermissionHost) relayTrustMap() map[string]any {
	m := map[string]any{}
	if h.relayTrust.CAFile != "" {
		m["ca_file"] = h.relayTrust.CAFile
	}
	certSHA := h.relayTrust.CertSHA256
	if certSHA == "" {
		certSHA = h.relayTrust.PeerSHA256
	}
	if certSHA != "" {
		m["cert_sha256"] = certSHA
	}
	if h.relayTrust.ServerName != "" {
		m["server_name"] = h.relayTrust.ServerName
	}
	return m
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
	delete(h.contextGenerations, c.owner+":"+c.contextID)
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
	for key := range h.contextGenerations {
		if strings.HasPrefix(key, session+":") {
			delete(h.contextGenerations, key)
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
	if budgets, ok := h.coordinator.ResponseBudgets(); ok {
		w.Header().Set("X-Octo-Acquisition-Budget-Bytes", strconv.FormatInt(budgets.MaxAcquisitionBytes, 10))
	}
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
