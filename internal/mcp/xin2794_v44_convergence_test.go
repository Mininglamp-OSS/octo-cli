package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func TestV44ServePermissionHostPackageV2PageBoundsAndRealTLS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	dir := secureTestDir(t)
	files, pin, _ := retainedPackageFixture(t)
	pkgDir := filepath.Join(dir, "pkg-v2")
	for relPath, raw := range files {
		target := filepath.Join(pkgDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0o400); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath := filepath.Join(pkgDir, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestSHA := sha256Sum(manifestBytes)
	if manifestSHA != "ecc56c2ab0a2c520ab7e5203d2abab06671daf90c28d69f88fe1de8d154109fc" || pin.Digest != manifestSHA {
		t.Fatal("unexpected v2 package manifest digest", manifestSHA)
	}

	serverCert, clientCert, roots, peerHash := controlCertificates(t)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[1]})
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	certPEM := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[0]}),
		caPEM...,
	)
	certPath := filepath.Join(dir, "server-cert.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(serverCert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "server-key.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}), 0o600); err != nil {
		t.Fatal(err)
	}

	f := newManagedFixture(t)
	// Switch f.backend to use serverCert signed by ca.pem so ServePermissionHost
	// verifies the origin backend via PeerCA/RelayCA over TLS.
	f.backend.Close()
	f.backend = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.backend.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}}
	f.backend.StartTLS()
	defer f.backend.Close()

	f.bundle.Key.Origin = f.backend.URL
	f.bundle.CatalogVersion = pin.Version
	f.bundle.CatalogDigest = pin.Digest
	f.bundle.Capability.Rules[0]["capabilities"] = []string{
		"octo-cli/message/op/message.send",
		"octo-cli/message/op/message.search",
		"octo-cli/docs/op/docs.search",
	}
	f.save()

	var searchMode atomic.Int32 // 0 = 2 valid pages, 1 = 101 items overflow
	var searchCalls atomic.Int32
	var docsCalls atomic.Int32
	baseServe := f.serve
	f.backend.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/messages/_search":
			page := searchCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if searchMode.Load() == 1 {
				items := make([]map[string]any, 101)
				for i := range items {
					items[i] = map[string]any{"message_id": strconv.Itoa(i + 1)}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data":       items,
					"pagination": map[string]any{"has_more": false, "next_cursor": ""},
				})
				return
			}
			if page == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data":       []map[string]any{{"message_id": "m1"}, {"message_id": "m2"}},
					"pagination": map[string]any{"has_more": true, "next_cursor": "cursor-2"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":       []map[string]any{{"message_id": "m3"}},
				"pagination": map[string]any{"has_more": false, "next_cursor": ""},
			})
		case "/v1/bot/docs/search":
			docsCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total":      1,
				"items":      []map[string]any{{"docId": "d1"}},
				"nextCursor": "",
			})
		default:
			baseServe(w, r)
		}
	})

	// Reserve free loopback ports for control and relay TLS listeners.
	reserveAddr := func() string {
		l, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			t.Fatal(lerr)
		}
		addr := l.Addr().String()
		_ = l.Close()
		return addr
	}
	controlAddr := reserveAddr()
	relayAddr := reserveAddr()
	readyPath := filepath.Join(dir, "ready.json")
	exePath := filepath.Join(dir, "fake-relay")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	leafSHA := sha256Sum(serverCert.Certificate[0])

	rawCfg := map[string]any{
		"version":              1,
		"origin":               f.backend.URL,
		"bot_uid":              "bot",
		"recipient_id":         "endpoint-1",
		"endpoint_incarnation": strings.Repeat("5", 32),
		"peer_sha256":          peerHash,
		"control_address":      controlAddr,
		"relay_address":        relayAddr,
		"relay_url":            "https://" + relayAddr,
		"certificate":          certPath,
		"private_key":          keyPath,
		"peer_ca":              caPath,
		"release_path":         manifestPath,
		"release_sha256":       manifestSHA,
		"ready_path":           readyPath,
		"relay_executable":     exePath,
		"relay_ca":             caPath,
		"relay_cert_sha256":    leafSHA,
		"relay_server_name":    "localhost",
		"authority_pins": map[string]any{
			"im":  map[string]any{"kind": "im", "incarnation": strings.Repeat("3", 32)},
			"sql": map[string]any{"kind": "sql", "incarnation": strings.Repeat("4", 32)},
		},
	}
	cfgBytes, err := permission.PrivateJSON(rawCfg)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "host.json")
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	loadedCfg, err := LoadPermissionHostConfig(cfgPath)
	if err != nil {
		t.Fatal("LoadPermissionHostConfig failed", err)
	}

	t.Setenv("SSL_CERT_FILE", caPath)
	admission := testAdmission(t)
	hostCtx, stopHost := context.WithCancel(ctx)
	hostDone := make(chan error, 1)
	go func() {
		hostDone <- servePermissionHostWithPackages(
			hostCtx,
			loadedCfg,
			"bf_managed",
			testRoot,
			admission,
			facadeBoth,
			cmdutil.GlobalOptions{},
			"",
			nil,
			map[string]permission.CatalogPin{manifestSHA: pin},
		)
	}()
	defer func() {
		stopHost()
		select {
		case herr := <-hostDone:
			if herr != nil {
				t.Errorf("ServePermissionHost exited with error: %v", herr)
			}
		case <-time.After(10 * time.Second):
			t.Error("ServePermissionHost did not shut down")
		}
	}()

	// Wait for ready.json to be written by ServePermissionHost.
	var ready struct {
		CoordinatorIncarnation string `json:"coordinator_incarnation"`
	}
	for deadline := time.Now().Add(120 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		select {
		case herr := <-hostDone:
			t.Fatalf("ServePermissionHost exited before readiness: %v", herr)
		default:
		}
		if b, rerr := os.ReadFile(readyPath); rerr == nil && json.Unmarshal(b, &ready) == nil && ready.CoordinatorIncarnation != "" {
			break
		}
	}
	if ready.CoordinatorIncarnation == "" {
		t.Fatal("ServePermissionHost did not publish readiness record")
	}

	conn, err := tls.Dial("tcp", controlAddr, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   "localhost",
		Certificates: []tls.Certificate{clientCert},
	})
	if err != nil {
		t.Fatal("control mTLS dial failed", err)
	}
	defer conn.Close()

	session := permission.NewID()
	request := func(method string, payload any) map[string]any {
		t.Helper()
		frame, ferr := permission.PrivateJSON(map[string]any{
			"control_version": 1,
			"request_id":      permission.NewID(),
			"session_id":      session,
			"method":          method,
			"payload":         payload,
		})
		if ferr != nil {
			t.Fatal(ferr)
		}
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(frame)))
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, werr := conn.Write(append(prefix[:], frame...)); werr != nil {
			t.Fatal(werr)
		}
		if _, rerr := io.ReadFull(conn, prefix[:]); rerr != nil {
			t.Fatal(rerr)
		}
		body := make([]byte, binary.BigEndian.Uint32(prefix[:]))
		if _, rerr := io.ReadFull(conn, body); rerr != nil {
			t.Fatal(rerr)
		}
		var resp map[string]any
		if json.Unmarshal(body, &resp) != nil || resp["status"] != "ok" {
			t.Fatalf("control %s failed: %s", method, string(body))
		}
		return resp["payload"].(map[string]any)
	}

	request("hello", map[string]any{
		"versions":             []int{2},
		"metadata_versions":    []int{2},
		"identity_versions":    []int{2},
		"endpoint_incarnation": loadedCfg.EndpointIncarnation,
		"configured_origin":    loadedCfg.Origin,
		"recipient_id":         loadedCfg.RecipientID,
	})
	ingress := request("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "delivery_v44_1",
		"stream":             f.header.Delivery,
		"payload_sha256":     f.header.PayloadSHA256,
	})
	contextID := permission.NewID()
	admitted := request("admit", map[string]any{
		"ingress_id":          ingress["ingress_id"],
		"proposed_context_id": contextID,
	})
	if admitted["scope_incarnation"] != f.bundle.Incarnation || admitted["context_epoch"] != f.bundle.ContextEpoch {
		t.Fatal("admit did not return authoritative scope_incarnation/context_epoch", admitted)
	}
	manifest := map[string]any{
		"context_id":         contextID,
		"context_generation": admitted["generation"],
		"originals":          []permission.NativeHeader{f.header},
	}
	mBytes, _ := permission.PrivateJSON(manifest)
	manifest["bytes"] = len(mBytes)
	manifest["sha256"] = sha256Sum(mBytes)
	request("bind_context", map[string]any{
		"capsule_id": admitted["capsule_id"],
		"manifest":   manifest,
	})
	opened := request("open_relay", map[string]any{
		"capsule_id":        admitted["capsule_id"],
		"recipient_id":      loadedCfg.RecipientID,
		"transport":         "stdio",
		"relay_nonce":       permission.NewID(),
		"include_tls_trust": true,
	})
	handleStr := opened["handle"].(string)
	tlsInfo, ok := opened["tls"].(map[string]any)
	if !ok || tlsInfo["ca_file"] != caPath || tlsInfo["cert_sha256"] != leafSHA || tlsInfo["server_name"] != "localhost" {
		t.Fatal("open_relay missing expected tls metadata", opened)
	}

	callViaTurnRelay := func(req rpcRequest) rpcResponse {
		t.Helper()
		rawReq, _ := json.Marshal(req)
		var out bytes.Buffer
		if rerr := ServeTurnRelayHandle(ctx, handleStr, bytes.NewReader(append(rawReq, '\n')), &out); rerr != nil {
			t.Fatal("ServeTurnRelayHandle failed over TLS 1.3:", rerr)
		}
		var resp rpcResponse
		if json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp) != nil {
			t.Fatal("invalid relay rpc response:", out.String())
		}
		return resp
	}

	if initResp := callViaTurnRelay(initializedRequest()); initResp.Error != nil {
		t.Fatal("initialize failed:", initResp.Error)
	}

	// 1. Positive --page-all on proven octo-cli/message/op/message.search across 2 pages (3 items total <= 100/page, <= 10 pages).
	searchParams, _ := json.Marshal(map[string]any{
		"name": "call_op",
		"arguments": map[string]any{
			"operation_id": "message.search",
			"arguments": map[string]any{
				"channel_id":   "group",
				"channel_type": 2,
				"keyword":      "plan",
				"page_all":     true,
			},
		},
	})
	searchReq := rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`10`), Method: "tools/call", Params: searchParams}
	searchResp := callViaTurnRelay(searchReq)
	if searchResp.Error != nil || bytes.Contains(searchResp.Result, []byte(`"isError":true`)) || searchCalls.Load() != 2 {
		t.Fatalf("proven message.search --page-all failed: err=%v result=%s calls=%d", searchResp.Error, string(searchResp.Result), searchCalls.Load())
	}

	// 2. Negative --page-all on message.search when backend returns 101 items (> 100 items/page bound) -> fails closed (policy_unavailable).
	searchMode.Store(1)
	overflowReq := rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`11`), Method: "tools/call", Params: searchParams}
	overflowResp := callViaTurnRelay(overflowReq)
	if overflowResp.Error == nil || overflowResp.Error.Code != -32007 || !strings.Contains(fmt.Sprint(overflowResp.Error.Data), "policy_unavailable") {
		t.Fatalf("message.search >100 items page bound did not fail closed: %+v", overflowResp)
	}

	// 3. Negative --page-all on unproven docs.search -> fails closed (policy_unavailable).
	docsParams, _ := json.Marshal(map[string]any{
		"name": "call_op",
		"arguments": map[string]any{
			"operation_id": "docs.search",
			"arguments": map[string]any{
				"q":        "plan",
				"page_all": true,
			},
		},
	})
	docsReq := rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`12`), Method: "tools/call", Params: docsParams}
	docsResp := callViaTurnRelay(docsReq)
	if docsResp.Error == nil || docsResp.Error.Code != -32007 || !strings.Contains(fmt.Sprint(docsResp.Error.Data), "policy_unavailable") {
		t.Fatalf("unproven docs.search --page-all did not fail closed: %+v", docsResp)
	}

	// 4. Negative TLS verification checks on ServeTurnRelay:
	//    (a) Wrong CA fails closed (never InsecureSkipVerify)
	otherServer, _, _, _ := controlCertificates(t)
	otherCAPath := filepath.Join(dir, "other-ca.pem")
	if err := os.WriteFile(otherCAPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherServer.Certificate[1]}), 0o600); err != nil {
		t.Fatal(err)
	}
	rawInit, _ := json.Marshal(initializedRequest())
	badCATicket, _ := json.Marshal(map[string]any{"ca_file": otherCAPath, "endpoint": loadedCfg.RelayURL, "token": strings.Repeat("a", 64)})
	var discard bytes.Buffer
	if err := ServeTurnRelay(ctx, bytes.NewReader(badCATicket), bytes.NewReader(append(rawInit, '\n')), &discard); err == nil {
		t.Fatal("ServeTurnRelay accepted untrusted private CA")
	}
	//    (b) Mismatched leaf cert_sha256 fails closed
	badPinTicket, _ := json.Marshal(map[string]any{"ca_file": caPath, "cert_sha256": strings.Repeat("f", 64), "endpoint": loadedCfg.RelayURL, "token": strings.Repeat("a", 64)})
	if err := ServeTurnRelay(ctx, bytes.NewReader(badPinTicket), bytes.NewReader(append(rawInit, '\n')), &discard); err == nil {
		t.Fatal("ServeTurnRelay accepted mismatched leaf cert_sha256")
	}
	//    (c) Mismatched SAN server_name fails closed
	badSANTicket, _ := json.Marshal(map[string]any{"ca_file": caPath, "cert_sha256": leafSHA, "endpoint": loadedCfg.RelayURL, "server_name": "wrong.invalid", "token": strings.Repeat("a", 64)})
	if err := ServeTurnRelay(ctx, bytes.NewReader(badSANTicket), bytes.NewReader(append(rawInit, '\n')), &discard); err == nil {
		t.Fatal("ServeTurnRelay accepted mismatched server_name SAN")
	}
}

func TestV44UnseenContributorResolutionAndSameGenerationContextReuse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	f := newManagedFixture(t)
	// Map 35 contributor delivery references (exercising >32 batching) plus 2 ingress deliveries.
	contribHeaders := make([]permission.NativeHeader, 35)
	byRef := map[string]permission.NativeHeader{}
	var contribEligible atomic.Bool
	contribEligible.Store(true)
	for i := range contribHeaders {
		h := f.header
		h.Reference = fmt.Sprintf("header_contrib_%02d", i)
		h.MessageID = strconv.Itoa(2000 + i)
		h.AuthorUID = "bob"
		h.Delivery.Sequence = strconv.Itoa(2000 + i)
		ref := fmt.Sprintf("contrib_deliv_%02d", i)
		contribHeaders[i] = h
		byRef[ref] = h
	}
	f.bundle.Invocation.Rules = append(f.bundle.Invocation.Rules, permission.Rule{
		"rule_id": "allow_bob_contrib",
		"effect":  "allow",
		"caller":  map[string]any{"kind": "uids", "uids": []string{"bob"}},
		"source":  map[string]any{"kind": "refs", "refs": []any{map[string]any{"kind": "group", "id": "group", "include_descendants": false}}},
	})
	f.save()

	turn2Header := f.header
	turn2Header.Reference = "header_turn_2"
	turn2Header.MessageID = "3002"
	turn2Header.Delivery.Sequence = "3002"
	byRef["ingress_turn_1"] = f.header
	byRef["ingress_turn_2"] = turn2Header
	turn3Header := f.header
	turn3Header.Reference = "header_turn_3"
	turn3Header.MessageID = "3003"
	turn3Header.Delivery.Sequence = "3003"
	byRef["ingress_turn_3"] = turn3Header

	var contributorPurposeCount atomic.Int32
	baseServe := f.serve
	f.backend.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/bot/permission-facts/v1" {
			baseServe(w, r)
			return
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		var req map[string]any
		_ = json.Unmarshal(bodyBytes, &req)
		purpose, _ := req["purpose"].(string)
		if purpose == "contributor" {
			contributorPurposeCount.Add(1)
		}
		rec := httptest.NewRecorder()
		baseServe(rec, r)
		var env map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Error(err)
			return
		}
		if results, ok := env["results"].([]any); ok {
			for _, item := range results {
				row := item.(map[string]any)
				ref, _ := row["reference"].(string)
				if h, exists := byRef[ref]; exists {
					row["header"] = h
					if strings.HasPrefix(ref, "contrib_deliv_") && !contribEligible.Load() {
						preds := row["predicates"].(map[string]any)
						preds["caller_current_read"] = false
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.Code)
		_ = json.NewEncoder(w).Encode(env)
	})

	serverCert, clientCert, roots, peerHash := controlCertificates(t)
	cfg := PermissionHostConfig{
		Version:             1,
		Origin:              f.backend.URL,
		BotUID:              "bot",
		RecipientID:         "endpoint-1",
		EndpointIncarnation: strings.Repeat("5", 32),
		PeerSHA256:          peerHash,
		RelayURL:            "https://127.0.0.1",
	}
	host, err := NewPermissionHost(cfg, f.coordinator, f.policies, f.reader, f.server)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    roots,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, aerr := listener.Accept()
		if aerr != nil {
			done <- aerr
			return
		}
		done <- host.ServeControl(ctx, conn.(*tls.Conn))
	}()
	conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   "localhost",
		Certificates: []tls.Certificate{clientCert},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(); <-done }()

	session := permission.NewID()
	sendControl := func(method string, payload any) map[string]any {
		t.Helper()
		frame, ferr := permission.PrivateJSON(map[string]any{
			"control_version": 1,
			"request_id":      permission.NewID(),
			"session_id":      session,
			"method":          method,
			"payload":         payload,
		})
		if ferr != nil {
			t.Fatal(ferr)
		}
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(frame)))
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, werr := conn.Write(append(prefix[:], frame...)); werr != nil {
			t.Fatal(werr)
		}
		if _, rerr := io.ReadFull(conn, prefix[:]); rerr != nil {
			t.Fatal(rerr)
		}
		body := make([]byte, binary.BigEndian.Uint32(prefix[:]))
		if _, rerr := io.ReadFull(conn, body); rerr != nil {
			t.Fatal(rerr)
		}
		var resp map[string]any
		if json.Unmarshal(body, &resp) != nil {
			t.Fatal(string(body))
		}
		return resp
	}

	if resp := sendControl("hello", map[string]any{
		"versions":             []int{2},
		"metadata_versions":    []int{2},
		"identity_versions":    []int{2},
		"endpoint_incarnation": cfg.EndpointIncarnation,
		"configured_origin":    cfg.Origin,
		"recipient_id":         cfg.RecipientID,
	}); resp["status"] != "ok" {
		t.Fatal(resp)
	}

	// Turn 1: Register ingress, admit, resolve first 33 unseen contributors via resolve_contributors (>32 batching),
	// and resolve remaining 2 unseen contributors via bind_context contributor_references.
	ing1 := sendControl("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "ingress_turn_1",
		"stream":             f.header.Delivery,
		"payload_sha256":     f.header.PayloadSHA256,
	})["payload"].(map[string]any)

	contextID := permission.NewID()
	admit1 := sendControl("admit", map[string]any{
		"ingress_id":          ing1["ingress_id"],
		"proposed_context_id": contextID,
	})["payload"].(map[string]any)
	gen1 := admit1["generation"].(string)
	if admit1["scope_incarnation"] != f.bundle.Incarnation || admit1["context_epoch"] != f.bundle.ContextEpoch {
		t.Fatal("admit1 missing scope_incarnation/context_epoch", admit1)
	}

	first33Refs := make([]string, 33)
	for i := 0; i < 33; i++ {
		first33Refs[i] = fmt.Sprintf("contrib_deliv_%02d", i)
	}
	resolvedResp := sendControl("resolve_contributors", map[string]any{
		"capsule_id":          admit1["capsule_id"],
		"delivery_references": first33Refs,
	})
	if resolvedResp["status"] != "ok" {
		t.Fatal("resolve_contributors failed", resolvedResp)
	}
	resolvedHeaders := resolvedResp["payload"].(map[string]any)["headers"].([]any)
	if len(resolvedHeaders) != 33 || contributorPurposeCount.Load() < 2 {
		t.Fatalf("expected 33 resolved headers across >=2 contributor batches, got len=%d batches=%d", len(resolvedHeaders), contributorPurposeCount.Load())
	}

	originals1 := make([]permission.NativeHeader, 0, 36)
	originals1 = append(originals1, f.header)
	originals1 = append(originals1, contribHeaders...)
	manifest1 := map[string]any{
		"context_id":         contextID,
		"context_generation": gen1,
		"originals":          originals1,
	}
	m1Bytes, _ := permission.PrivateJSON(manifest1)
	manifest1["bytes"] = len(m1Bytes)
	manifest1["sha256"] = sha256Sum(m1Bytes)
	bind1 := sendControl("bind_context", map[string]any{
		"capsule_id":             admit1["capsule_id"],
		"manifest":               manifest1,
		"contributor_references": []string{"contrib_deliv_33", "contrib_deliv_34"},
	})
	if bind1["status"] != "ok" {
		t.Fatal("bind_context with unseen contributor_references failed", bind1)
	}
	if close1 := sendControl("close", map[string]any{"capsule_id": admit1["capsule_id"], "reason": "completed"}); close1["status"] != "ok" {
		t.Fatal(close1)
	}

	// Turn 2: Same proposed_context_id under same (key, scope_incarnation, context_epoch)
	// reuses verified context generation gen1 and revalidates all contributors.
	ing2 := sendControl("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "ingress_turn_2",
		"stream":             turn2Header.Delivery,
		"payload_sha256":     turn2Header.PayloadSHA256,
	})["payload"].(map[string]any)
	admit2 := sendControl("admit", map[string]any{
		"ingress_id":          ing2["ingress_id"],
		"proposed_context_id": contextID,
		"prior_generation":    gen1,
	})["payload"].(map[string]any)
	if admit2["generation"] != gen1 {
		t.Fatalf("expected same-generation reuse %s, got %v", gen1, admit2["generation"])
	}
	originals2 := append([]permission.NativeHeader{turn2Header}, originals1...)
	manifest2 := map[string]any{
		"context_id":         contextID,
		"context_generation": gen1,
		"originals":          originals2,
	}
	m2Bytes, _ := permission.PrivateJSON(manifest2)
	manifest2["bytes"] = len(m2Bytes)
	manifest2["sha256"] = sha256Sum(m2Bytes)
	bind2 := sendControl("bind_context", map[string]any{
		"capsule_id": admit2["capsule_id"],
		"manifest":   manifest2,
	})
	if bind2["status"] != "ok" {
		t.Fatal("bind_context turn 2 failed", bind2)
	}
	if close2 := sendControl("close", map[string]any{"capsule_id": admit2["capsule_id"], "reason": "completed"}); close2["status"] != "ok" {
		t.Fatal(close2)
	}

	// Turn 3: Rotate context_epoch (and mark contributors ineligible) -> admit mints a fresh generation != gen1,
	// and attempting to bind with ineligible contributors refuses with context_untrusted.
	f.mu.Lock()
	f.bundle.Revision = "1"
	f.bundle.ContextEpoch = "1"
	f.save()
	f.mu.Unlock()
	contribEligible.Store(false)

	ing3 := sendControl("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "ingress_turn_3",
		"stream":             turn3Header.Delivery,
		"payload_sha256":     turn3Header.PayloadSHA256,
	})["payload"].(map[string]any)
	admit3 := sendControl("admit", map[string]any{
		"ingress_id":          ing3["ingress_id"],
		"proposed_context_id": contextID,
	})["payload"].(map[string]any)
	gen3 := admit3["generation"].(string)
	if gen3 == gen1 || admit3["context_epoch"] != "1" {
		t.Fatalf("expected fresh generation after context_epoch rotation, got gen=%s epoch=%v", gen3, admit3["context_epoch"])
	}
	originals3 := []permission.NativeHeader{turn3Header, contribHeaders[0]}
	manifest3 := map[string]any{
		"context_id":         contextID,
		"context_generation": gen3,
		"originals":          originals3,
	}
	m3Bytes, _ := permission.PrivateJSON(manifest3)
	manifest3["bytes"] = len(m3Bytes)
	manifest3["sha256"] = sha256Sum(m3Bytes)
	bind3 := sendControl("bind_context", map[string]any{
		"capsule_id": admit3["capsule_id"],
		"manifest":   manifest3,
	})
	if bind3["status"] != "refused" || !strings.Contains(fmt.Sprint(bind3["payload"]), "context_untrusted") {
		t.Fatalf("expected context_untrusted when contributor ineligible, got %+v", bind3)
	}
}
