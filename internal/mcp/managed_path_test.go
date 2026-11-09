package mcp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

type managedFixture struct {
	t            *testing.T
	backend      *httptest.Server
	mu           sync.Mutex
	bundle       permission.Bundle
	policy       []byte
	header       permission.NativeHeader
	eligible     atomic.Bool
	nativeReads  atomic.Int32
	policyReads  atomic.Int32
	business     atomic.Int32
	serial       atomic.Int32
	badAuthority atomic.Bool
	badProfile   atomic.Bool
	reader       *permission.NativeReader
	policies     *permission.SelfPolicyReader
	coordinator  *permission.Coordinator
	profiles     map[string][]string
}

func newManagedFixture(t *testing.T) *managedFixture {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("managed host private storage and owned sinks require POSIX")
	}
	f := &managedFixture{t: t}
	f.eligible.Store(true)
	shared, err := osReadSharedProfiles()
	if err != nil {
		t.Fatal(err)
	}
	f.profiles = shared
	f.header.Version = 1
	f.header.Reference = "header"
	f.header.MessageID = "42"
	f.header.Source.Kind = "group"
	f.header.Source.ID = "group"
	f.header.SpaceID = "space"
	f.header.AuthorUID = "alice"
	f.header.AuthorKind = "human"
	f.header.BotUID = "bot"
	f.header.MessageVersion = "message_version"
	f.header.PayloadSHA256 = strings.Repeat("a", 64)
	f.header.Delivery.StreamID = "stream"
	f.header.Delivery.Incarnation = strings.Repeat("1", 32)
	f.header.Delivery.Sequence = "1"
	f.backend = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.backend.Close)
	caps := []string{"octo-cli/message/op/message.send", "octo-cli/message/schema/message.send", "octo-cli/discovery/module-map", "octo-cli/catalog/resource-list/skills", "octo-cli/skills/resource/octo-messaging/SKILL.md", "octo-cli/docs/op/docs.scene.export", "octo-cli/docs/op/docs.scene.get", "octo-cli/docs/op/docs.attachments.get", "octo-cli/docs/derived/portable-attachment-bytes"}
	rule := func(id string, capability bool) permission.Rule {
		r := permission.Rule{"rule_id": id, "effect": "allow", "caller": map[string]any{"kind": "all_humans"}, "source": map[string]any{"kind": "all_in_space", "conversation_types": []any{"group"}}}
		if capability {
			r["capabilities"] = caps
		}
		return r
	}
	f.bundle = permission.Bundle{SchemaVersion: 1, Key: permission.Key{Origin: f.backend.URL, BotUID: "bot", SpaceID: "space"}, Incarnation: strings.Repeat("2", 32), Revision: "0", ContextEpoch: "0", CatalogVersion: "synthetic-managed-v1", CatalogDigest: strings.Repeat("b", 64), Invocation: permission.Stage{State: "E", Rules: []permission.Rule{rule("admit", false)}}, Capability: permission.Stage{State: "E", Rules: []permission.Rule{rule("capability", true)}}}
	f.save()
	conf := f.backend.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	conf.InsecureSkipVerify = false
	f.policies, err = permission.NewSelfPolicyReader(f.backend.URL, "bot", "bf_managed", conf, f.backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	f.reader, err = permission.NewNativeReader(f.policies, map[string]permission.AuthorityPin{"im": {Kind: "im", Incarnation: strings.Repeat("3", 32)}, "sql": {Kind: "sql", Incarnation: strings.Repeat("4", 32)}})
	if err != nil {
		t.Fatal(err)
	}
	f.coordinator, err = permission.NewCoordinator(permission.Catalog{Version: f.bundle.CatalogVersion, Digest: f.bundle.CatalogDigest, Capabilities: caps}, f.policies, f.reader)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func osReadSharedProfiles() (map[string][]string, error) {
	var index sourceIndex
	if err := json.Unmarshal(catalogSourceIndex, &index); err != nil {
		return nil, err
	}
	// The unchanged shared contract is independently retained in this checkout.
	b, err := os.ReadFile("../permission/contracts/shared-contract-v6.json")
	if err != nil {
		return nil, err
	}
	var s struct {
		Profiles map[string][]string `json:"predicate_profiles"`
	}
	err = json.Unmarshal(b, &s)
	return s.Profiles, err
}
func (f *managedFixture) save() {
	f.t.Helper()
	_, raw, err := permission.Seal(f.bundle)
	if err != nil {
		f.t.Fatal(err)
	}
	f.policy = raw
}
func (f *managedFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer bf_managed" {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/v1/auth/verify-bot":
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		if p["identity_version"] != float64(2) {
			f.t.Error("managed verification downgraded")
		}
		response := map[string]any{"identity_version": 2, "principal_kind": "user_bot", "bot_uid": "bot"}
		if p["include_mcp_policy"] == true {
			response["mcp_policy"] = json.RawMessage(compatiblePolicy)
		}
		_ = json.NewEncoder(w).Encode(response)
	case "/v1/bot/permission-bundle/v1":
		f.policyReads.Add(1)
		f.mu.Lock()
		defer f.mu.Unlock()
		_, _ = w.Write(f.policy)
	case "/v1/bot/permission-facts/v1":
		f.nativeReads.Add(1)
		var request struct {
			RequestID  string `json:"request_id"`
			Purpose    string
			References []struct {
				DeliveryReference string `json:"delivery_reference"`
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		authorities := []map[string]any{}
		nonces := []string{}
		for _, name := range []string{"im", "sql"} {
			nonce := fmt.Sprintf("%032x", f.serial.Add(1))
			nonces = append(nonces, nonce)
			inc := "3"
			if name == "sql" {
				inc = "4"
			}
			if f.badAuthority.Load() {
				name = "unknown"
			}
			authorities = append(authorities, map[string]any{"authority_id": name, "kind": name, "incarnation": strings.Repeat(inc, 32), "position": "position", "read_nonce": nonce, "consistency": "ack_before_read_start", "started_at": time.Now().UTC().Format(time.RFC3339Nano), "completed_at": time.Now().UTC().Format(time.RFC3339Nano)})
		}
		results := []map[string]any{}
		for _, ref := range request.References {
			profile := "group_" + request.Purpose + "_v1"
			if f.badProfile.Load() {
				profile = "dm_" + request.Purpose + "_v1"
			}
			pred := map[string]bool{}
			for _, atom := range f.profiles[profile] {
				pred[atom] = f.eligible.Load()
			}
			results = append(results, map[string]any{"reference": ref.DeliveryReference, "status": "resolved", "header": f.header, "profile": profile, "authority_refs": nonces, "predicates": pred})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "profile_version": 1, "key": f.bundle.Key, "request_id": request.RequestID, "read_id": fmt.Sprintf("%032x", f.serial.Add(1)), "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "authorities": authorities, "results": results})
	case "/v1/bot/sendMessage":
		f.business.Add(1)
		_, _ = w.Write([]byte(`{"message_id":"synthetic-result"}`))
	case "/v1/bot/docs/board/scene":
		f.business.Add(1)
		_, _ = w.Write([]byte(`{"elements":[],"files":{},"appState":{},"baseVersion":"synthetic-version"}`))
	default:
		http.NotFound(w, r)
	}
}

func (f *managedFixture) turn() *ManagedTurn {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o, err := f.reader.Resolve(ctx, f.bundle.Key, "admit", []string{"delivery"})
	if err != nil {
		f.t.Fatal(err)
	}
	generation := permission.NewID()
	if err = f.reader.BindGeneration(generation, o[0], nil); err != nil {
		f.t.Fatal(err)
	}
	i, err := f.coordinator.Register(permission.Binding{Key: f.bundle.Key, Incarnation: f.bundle.Incarnation, ContextEpoch: "0", Generation: generation, Subject: permission.Subject{Caller: "alice", Kind: "group", Source: "group"}})
	if err == nil {
		err = f.coordinator.Admit(ctx, i)
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return NewManagedTurn(f.coordinator, i, permission.DeclarationV2{Sender: "alice", Channel: "group", ChannelType: 2, SpaceID: "space", MessageID: "42"})
}
func (f *managedFixture) server(turn *ManagedTurn) *Server {
	s := newTestServer(f.t).WithFacade(facadeBoth).WithManagedTurn(turn).WithTrustedContext(TrustedContext{SpaceID: "space"})
	s.factoryFn = fakeBackendFactory(f.backend.URL, "bf_managed")
	return s
}
func initializedRequest() rpcRequest {
	return rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(`{"capabilities":{"experimental":{"octo_permission_binding":{"selected_binding":2,"selected_metadata":2,"selected_identity":2}}}}`)}
}
func sendRequest(t *testing.T, id int, tool string, source permission.DeclarationV2) rpcRequest {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": tool, "arguments": map[string]any{"operation_id": "message.send", "arguments": map[string]any{"channel_id": "target", "channel_type": 2, "payload": map[string]any{"text": "synthetic"}}}})
	req := rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(fmt.Sprint(id)), Method: "tools/call", Params: params}
	if err := relayDeclaration(&req, source); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestManagedMCPRealHTTPAndStdioPositiveDeniedAndReplay(t *testing.T) {
	for _, transport := range []string{"http", "stdio"} {
		for _, tool := range []string{"execute", "call_op"} {
			t.Run(transport+"/"+tool, func(t *testing.T) {
				f := newManagedFixture(t)
				turn := f.turn()
				defer turn.Close()
				server := f.server(turn)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if resp, _ := server.Dispatch(ctx, initializedRequest()); resp.Error != nil {
					t.Fatal(resp.Error)
				}
				run := func(req rpcRequest) rpcResponse {
					var out bytes.Buffer
					if transport == "stdio" {
						raw, _ := json.Marshal(req)
						server.handleStdioLine(ctx, raw, &out)
					} else {
						resp, has, call := server.dispatchManaged(ctx, req)
						if !has {
							t.Fatal("no response")
						}
						if err := writeManaged(ctx, &out, resp, call); err != nil {
							t.Fatal(err)
						}
					}
					var response rpcResponse
					if json.Unmarshal(out.Bytes(), &response) != nil {
						t.Fatal(out.String())
					}
					return response
				}
				req := sendRequest(t, 2, tool, turn.source)
				positive := run(req)
				if positive.Error != nil {
					t.Fatal(positive.Error)
				}
				if f.business.Load() != 1 || bytes.Contains(positive.Result, []byte(`"isError":true`)) {
					t.Fatal("positive failed", string(positive.Result), f.business.Load())
				}
				if replay := run(req); replay.Error == nil || replay.Error.Code != -32007 || f.business.Load() != 1 {
					t.Fatal("replay reached business", replay)
				}
				f.mu.Lock()
				f.bundle.Revision = "1"
				f.bundle.Capability.Rules = []permission.Rule{}
				f.save()
				f.mu.Unlock()
				denied := run(sendRequest(t, 3, tool, turn.source))
				if denied.Error == nil || denied.Error.Code != -32007 || f.business.Load() != 1 || !strings.Contains(fmt.Sprint(denied.Error.Data), "capability_denied") {
					t.Fatal("denial", denied, f.business.Load())
				}
				if f.nativeReads.Load() < 4 || f.policyReads.Load() < 4 {
					t.Fatal("fresh facts/policy not traversed")
				}
			})
		}
	}
}

func TestManagedNativeRemovalAndProtectedResources(t *testing.T) {
	f := newManagedFixture(t)
	turn := f.turn()
	defer turn.Close()
	s := f.server(turn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.Dispatch(ctx, initializedRequest())
	req := rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`2`), Method: "resources/read", Params: json.RawMessage(`{"uri":"octo://skills/octo-messaging/SKILL.md#anchor"}`)}
	relayDeclaration(&req, turn.source)
	r, _ := s.Dispatch(ctx, req)
	if r.Error != nil || !bytes.Contains(r.Result, []byte("text/markdown")) {
		t.Fatal(r)
	}
	f.eligible.Store(false)
	r, _ = s.Dispatch(ctx, sendRequest(t, 3, "execute", turn.source))
	if r.Error == nil || r.Error.Code != -32007 || f.business.Load() != 0 {
		t.Fatal("native removal bypassed", r)
	}
}

func controlCertificates(t *testing.T) (serverCert, clientCert tls.Certificate, roots *x509.CertPool, peerHash string) {
	t.Helper()
	_, caKey, _ := ed25519.GenerateKey(rand.Reader)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	roots = x509.NewCertPool()
	roots.AddCert(ca)
	makeCert := func(serial int64, usage x509.ExtKeyUsage) tls.Certificate {
		pub, key, _ := ed25519.GenerateKey(rand.Reader)
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "synthetic-peer"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		der, err := x509.CreateCertificate(rand.Reader, template, ca, pub, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}
	}
	server, client := makeCert(2, x509.ExtKeyUsageServerAuth), makeCert(3, x509.ExtKeyUsageClientAuth)
	sum := sha256.Sum256(client.Certificate[0])
	return server, client, roots, hex.EncodeToString(sum[:])
}

func TestManagedPrivateControlToHTTPRelay(t *testing.T) {
	f := newManagedFixture(t)
	serverCert, clientCert, roots, peerHash := controlCertificates(t)
	cfg := PermissionHostConfig{Version: 1, Origin: f.backend.URL, BotUID: "bot", RecipientID: "endpoint-1", EndpointIncarnation: strings.Repeat("5", 32), PeerSHA256: peerHash}
	host, err := NewPermissionHost(cfg, f.coordinator, f.policies, f.reader, f.server)
	if err != nil {
		t.Fatal(err)
	}
	relay := httptest.NewServer(host)
	defer relay.Close()
	host.config.RelayURL = relay.URL
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		done <- host.ServeControl(context.Background(), conn.(*tls.Conn))
	}()
	conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{clientCert}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { conn.Close(); <-done }()
	session := permission.NewID()
	request := func(method string, payload any) map[string]any {
		frame, _ := permission.PrivateJSON(map[string]any{"control_version": 1, "request_id": permission.NewID(), "session_id": session, "method": method, "payload": payload})
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(frame)))
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(append(prefix[:], frame...)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			t.Fatal(err)
		}
		body := make([]byte, binary.BigEndian.Uint32(prefix[:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if json.Unmarshal(body, &response) != nil || response["status"] != "ok" {
			t.Fatal("control", method, string(body))
		}
		return response["payload"].(map[string]any)
	}
	request("hello", map[string]any{"versions": []int{2}, "metadata_versions": []int{2}, "identity_versions": []int{2}, "endpoint_incarnation": cfg.EndpointIncarnation, "configured_origin": cfg.Origin, "recipient_id": cfg.RecipientID})
	ingress := request("register_ingress", map[string]any{"key": f.bundle.Key, "delivery_reference": "delivery", "stream": f.header.Delivery, "payload_sha256": f.header.PayloadSHA256})
	contextID := permission.NewID()
	admitted := request("admit", map[string]any{"ingress_id": ingress["ingress_id"], "context_id": contextID})
	manifest := map[string]any{"context_id": contextID, "generation": admitted["generation"], "delivery_references": []string{}}
	b, _ := permission.PrivateJSON(manifest)
	request("bind_context", map[string]any{"capsule_id": admitted["capsule_id"], "manifest": manifest, "manifest_sha256": sha256Sum(b)})
	opened := request("open_relay", map[string]any{"capsule_id": admitted["capsule_id"], "recipient_id": cfg.RecipientID, "transport": "http", "relay_nonce": permission.NewID()})
	post := func(req rpcRequest) rpcResponse {
		raw, _ := json.Marshal(req)
		request, _ := http.NewRequest("POST", relay.URL, bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer "+opened["token"].(string))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var r rpcResponse
		if json.NewDecoder(response.Body).Decode(&r) != nil {
			t.Fatal("relay response")
		}
		return r
	}
	if r := post(initializedRequest()); r.Error != nil {
		t.Fatal(r.Error)
	}
	if r := post(sendRequest(t, 2, "execute", permission.DeclarationV2{Sender: "alice", Channel: "group", ChannelType: 2, SpaceID: "space", MessageID: "42"})); r.Error != nil || f.business.Load() != 1 {
		t.Fatal(r, f.business.Load())
	}
	// The worker receives only the relay ticket, never the host/Bot secret.
	ticket, _ := json.Marshal(map[string]any{"endpoint": relay.URL, "token": opened["token"]})
	workerRequest := sendRequest(t, 3, "call_op", permission.DeclarationV2{Sender: "alice", Channel: "group", ChannelType: 2, SpaceID: "space", MessageID: "42"})
	workerBytes, _ := json.Marshal(workerRequest)
	var workerOutput bytes.Buffer
	if err := ServeTurnRelay(context.Background(), bytes.NewReader(ticket), bytes.NewReader(append(workerBytes, '\n')), &workerOutput); err != nil {
		t.Fatal(err)
	}
	if f.business.Load() != 2 || !json.Valid(bytes.TrimSpace(workerOutput.Bytes())) {
		t.Fatal("stdio relay", workerOutput.String(), f.business.Load())
	}
	forged := sendRequest(t, 4, "execute", permission.DeclarationV2{Sender: "mallory", Channel: "group", ChannelType: 2, SpaceID: "space", MessageID: "42"})
	if result := post(forged); result.Error == nil || result.Error.Code != -32007 || f.business.Load() != 2 {
		t.Fatal("forged declaration", result)
	}
	request("close", map[string]any{"capsule_id": admitted["capsule_id"], "reason": "completed"})
	if len(host.relays) != 0 {
		t.Fatal("relay survived close")
	}
}

func TestManagedUnknownAuthorityAndWrongProfileNeverReachBusiness(t *testing.T) {
	for _, profile := range []bool{false, true} {
		t.Run(fmt.Sprint(profile), func(t *testing.T) {
			f := newManagedFixture(t)
			turn := f.turn()
			defer turn.Close()
			s := f.server(turn)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s.Dispatch(ctx, initializedRequest())
			if profile {
				f.badProfile.Store(true)
			} else {
				f.badAuthority.Store(true)
			}
			result, _ := s.Dispatch(ctx, sendRequest(t, 2, "execute", turn.source))
			if result.Error == nil || result.Error.Code != -32007 || f.business.Load() != 0 {
				t.Fatal(result, f.business.Load())
			}
		})
	}
}

func TestPrivateManifestDescriptorSupportsMaximumAndRejectsReplacement(t *testing.T) {
	h := &PermissionHost{capsules: map[string]*hostCapsule{}}
	c := &hostCapsule{id: "capsule"}
	h.capsules[c.id] = c
	// Exercise the full 1 MiB staging domain without exceeding a control frame.
	manifest := []byte(`{"pad":"` + strings.Repeat("x", (1<<20)-10) + `"}`)
	digest := sha256Sum(manifest)
	for offset := 0; offset < len(manifest); offset += 32768 {
		end := offset + 32768
		if end > len(manifest) {
			end = len(manifest)
		}
		p := map[string]any{"manifest_sha256": digest, "manifest_chunk": map[string]any{"offset": json.Number(strconv.Itoa(offset)), "total_bytes": json.Number(strconv.Itoa(len(manifest))), "data_base64": base64.StdEncoding.EncodeToString(manifest[offset:end])}}
		b, pending, err := h.contextManifest(c, p)
		if err != nil || pending != (end < len(manifest)) || len(b) != end {
			t.Fatal(offset, pending, len(b), err)
		}
	}
	if _, _, err := h.contextManifest(c, map[string]any{"manifest": map[string]any{}}); err == nil {
		t.Fatal("manifest replaced after staging")
	}
}

func TestUnprovenCatalogCannotBeEnabledByOperatorHash(t *testing.T) {
	path := t.TempDir() + "/candidate.json"
	candidate := []byte(`{"format":"permission-release-manifest-v1"}`)
	if err := os.WriteFile(path, candidate, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSupportedRelease(path, sha256Sum(candidate)); err == nil {
		t.Fatal("operator hash promoted unsupported source")
	}
}
