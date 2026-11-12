package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func TestXIN2794V45ConfigurableResponseBudgetsHTTPAndStdio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Verify fail-closed validation on invalid/partial ResponseBudgets and StaticProvenanceRequired isolation.
	if !permission.StaticProvenanceRequired("octo-cli/docs/derived/portable-attachment-bytes") {
		t.Fatal("expected portable-attachment-bytes to require static source provenance")
	}
	if permission.StaticProvenanceRequired("octo-cli/docs/op/docs.search") {
		t.Fatal("did not expect docs.search to require static binary attachment provenance")
	}
	f0 := newManagedFixture(t)
	if err := f0.coordinator.ConfigureResponseBudgets(permission.ResponseBudgets{
		MaxAcquisitionBytes:   2048,
		MaxPageItems:          10,
		MaxPages:              3,
		MaxCumulativeBytes:    1024, // < MaxAcquisitionBytes -> invalid
		MaxSerializedRPCBytes: 4096,
	}); err == nil {
		t.Fatal("expected invalid ResponseBudgets (MaxCumulativeBytes < MaxAcquisitionBytes) to fail closed")
	}

	// 2. Stand up a managed fixture with controllable backend endpoints for single-op, paginated, and mutating calls.
	f := newManagedFixture(t)
	caps := []string{
		"octo-cli/message/op/message.send",
		"octo-cli/message/op/message.search",
		"octo-cli/docs/op/docs.search",
		"octo-cli/docs/op/docs.attachments.get",
		"octo-cli/docs/derived/portable-attachment-bytes",
	}
	f.bundle.Capability.Rules[0]["capabilities"] = caps
	f.save()
	var err error
	f.coordinator, err = permission.NewCoordinator(
		permission.Catalog{
			Version:      f.bundle.CatalogVersion,
			Digest:       f.bundle.CatalogDigest,
			Capabilities: caps,
		},
		f.policies,
		f.reader,
	)
	if err != nil {
		t.Fatal(err)
	}

	var docsMode atomic.Int32
	var docsCalls atomic.Int32
	var sendMode atomic.Int32
	var sendCalls atomic.Int32
	var factsCalls atomic.Int32

	baseServe := f.serve
	f.backend.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/permission-facts/v1":
			factsCalls.Add(1)
			baseServe(w, r)
		case "/v1/bot/docs/search":
			callNum := docsCalls.Add(1)
			var reqBody map[string]any
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			cursor, _ := reqBody["cursor"].(string)
			w.Header().Set("Content-Type", "application/json")
			switch docsMode.Load() {
			case 0:
				// Within budget: 2 pages, 2 items per page, nextCursor="" on page 2.
				if cursor == "" {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"total":      4,
						"items":      []map[string]any{{"docId": "d1"}, {"docId": "d2"}},
						"nextCursor": "c2",
					})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total":      4,
					"items":      []map[string]any{{"docId": "d3"}, {"docId": "d4"}},
					"nextCursor": "",
				})
			case 1:
				// Clean page-budget-prefix stop: 3+ pages available, stops cleanly at MaxPages=2 with PAGE_BUDGET_PREFIX.
				if cursor == "" {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"total":      6,
						"items":      []map[string]any{{"docId": "p1_1"}, {"docId": "p1_2"}},
						"nextCursor": "c2",
					})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total":      6,
					"items":      []map[string]any{{"docId": "p2_1"}, {"docId": "p2_2"}},
					"nextCursor": "c3",
				})
			case 2:
				// Page item overflow: returns 4 items > MaxPageItems=3.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total": 4,
					"items": []map[string]any{
						{"docId": "o1"}, {"docId": "o2"}, {"docId": "o3"}, {"docId": "o4"},
					},
					"nextCursor": "",
				})
			case 3:
				// Cumulative byte overflow across pages: each page is <= MaxAcquisitionBytes (1200),
				// but 2 pages combined exceed MaxCumulativeBytes (1400).
				pad := strings.Repeat("x", 720)
				if cursor == "" {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"total":      2,
						"items":      []map[string]any{{"docId": "b1", "snippet": pad}},
						"nextCursor": "c2",
					})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total":      2,
					"items":      []map[string]any{{"docId": "b2", "snippet": pad}},
					"nextCursor": "",
				})
			case 4:
				// Single-acquisition byte overflow: single response exceeds MaxAcquisitionBytes (1200).
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total":      1,
					"items":      []map[string]any{{"docId": "huge", "snippet": strings.Repeat("y", 1500)}},
					"nextCursor": "",
				})
			case 5:
				// Serialized RPC response overflow: response is <= MaxAcquisitionBytes (1200) and <= MaxCumulativeBytes (1400),
				// but when wrapped in MCP JSON-RPC envelope (with quotes that expand under JSON string escaping)
				// exceeds MaxSerializedRPCBytes (1450).
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total":      1,
					"items":      []map[string]any{{"docId": "rpc_overflow", "snippet": strings.Repeat(`"`, 450)}},
					"nextCursor": "",
				})
			default:
				t.Fatalf("unexpected docsMode %d on call %d", docsMode.Load(), callNum)
			}
		case "/v1/bot/sendMessage":
			sendCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			switch sendMode.Load() {
			case 0:
				_, _ = w.Write([]byte(`{"message_id":"msg_ok"}`))
			case 1:
				// Mutating POST response exceeds MaxAcquisitionBytes (1200). Must NOT retry/replay!
				_ = json.NewEncoder(w).Encode(map[string]any{
					"message_id": "msg_acq_overflow",
					"echo":       strings.Repeat("z", 1600),
				})
			case 2:
				// Mutating POST response fits MaxAcquisitionBytes (1200), but serialized MCP RPC response exceeds MaxSerializedRPCBytes (1450).
				_ = json.NewEncoder(w).Encode(map[string]any{
					"message_id": "msg_rpc_overflow",
					"echo":       strings.Repeat(`"`, 450),
				})
			}
		default:
			baseServe(w, r)
		}
	})

	budgets := permission.ResponseBudgets{
		MaxAcquisitionBytes:   1200,
		MaxPageItems:          3,
		MaxPages:              2,
		MaxCumulativeBytes:    1400,
		MaxSerializedRPCBytes: 1450,
	}
	if err := f.coordinator.ConfigureResponseBudgets(budgets); err != nil {
		t.Fatalf("ConfigureResponseBudgets failed: %v", err)
	}
	if got, ok := f.coordinator.ResponseBudgets(); !ok || got != budgets {
		t.Fatalf("ResponseBudgets readback mismatch: got=%+v ok=%v", got, ok)
	}

	serverCert, _, roots, peerHash := controlCertificates(t)
	dir := secureTestDir(t)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[1]})
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", caPath)

	rawRelay, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rawRelay.Close()
	securedRelay := permission.PublicationTLSListener(rawRelay, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}})
	relayURL := "https://" + rawRelay.Addr().String()
	exePath := filepath.Join(dir, "fake-relay")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	leafSHA := sha256Sum(serverCert.Certificate[0])
	caSHA := sha256Sum(caPEM)
	cfg := PermissionHostConfig{
		Version:             1,
		Origin:              f.backend.URL,
		BotUID:              "bot",
		RecipientID:         "endpoint-1",
		EndpointIncarnation: strings.Repeat("5", 32),
		PeerSHA256:          peerHash,
		RelayURL:            relayURL,
		RelayExecutable:     exePath,
		RelayCA:             caPath,
		RelayCASHA256:       caSHA,
		RelayPeerSHA256:     leafSHA,
		RelayCertSHA256:     leafSHA,
		RelayServerName:     "localhost",
		ResponseBudgets:     &budgets,
	}
	host, err := NewPermissionHost(cfg, f.coordinator, f.policies, f.reader, f.server)
	if err != nil {
		t.Fatal("NewPermissionHost failed:", err)
	}
	relayServer := &http.Server{Handler: host, ReadHeaderTimeout: 5 * time.Second}
	defer relayServer.Close()
	go func() { _ = relayServer.Serve(securedRelay) }()

	// Issue a managed turn token and stdio relay handle.
	turn := f.turn()
	defer turn.Close()
	token := strings.Repeat("b", 64)
	host.mu.Lock()
	host.relays[token] = &hostRelay{token: token, turn: turn, capsule: &hostCapsule{id: permission.NewID(), bound: true}}
	host.mu.Unlock()

	stdioHandle, err := TurnRelayHandleWithTrust(relayURL, token, host.relayTrust)
	if err != nil {
		t.Fatal("TurnRelayHandleWithTrust failed:", err)
	}
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "localhost"},
		},
	}

	callHTTP := func(req rpcRequest) rpcResponse {
		t.Helper()
		raw, _ := json.Marshal(req)
		httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, relayURL, bytes.NewReader(raw))
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, derr := httpClient.Do(httpReq)
		if derr != nil {
			t.Fatal("HTTP relay call failed:", derr)
		}
		defer resp.Body.Close()
		var out rpcResponse
		if json.NewDecoder(resp.Body).Decode(&out) != nil {
			t.Fatal("invalid HTTP relay response")
		}
		return out
	}
	callStdio := func(req rpcRequest) rpcResponse {
		t.Helper()
		raw, _ := json.Marshal(req)
		var out bytes.Buffer
		if serr := ServeTurnRelayHandle(ctx, stdioHandle, bytes.NewReader(append(raw, '\n')), &out); serr != nil {
			t.Fatal("ServeTurnRelayHandle failed:", serr)
		}
		var resp rpcResponse
		if json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp) != nil {
			t.Fatal("invalid stdio relay response:", out.String())
		}
		return resp
	}

	if r := callHTTP(initializedRequest()); r.Error != nil {
		t.Fatal("initialize failed:", r.Error)
	}

	for _, transport := range []string{"http", "stdio"} {
		sendRPC := callHTTP
		if transport == "stdio" {
			sendRPC = callStdio
		}
		for _, toolName := range []string{"call_op", "execute"} {
			t.Run(transport+"_"+toolName, func(t *testing.T) {
				makeDocsSearchReq := func(id int, pageAll bool) rpcRequest {
					args := map[string]any{"q": "architecture"}
					if pageAll {
						args["page_all"] = true
					}
					params, _ := json.Marshal(map[string]any{
						"name": toolName,
						"arguments": map[string]any{
							"operation_id": "docs.search",
							"arguments":    args,
						},
					})
					return rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(fmt.Sprintf(`%d`, id)), Method: "tools/call", Params: params}
				}
				makeMessageSendReq := func(id int) rpcRequest {
					params, _ := json.Marshal(map[string]any{
						"name": toolName,
						"arguments": map[string]any{
							"operation_id": "message.send",
							"arguments": map[string]any{
								"channel_id":   "target",
								"channel_type": 2,
								"payload":      map[string]any{"text": "hello"},
							},
						},
					})
					return rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(fmt.Sprintf(`%d`, id)), Method: "tools/call", Params: params}
				}

				// (a) Positive 2-page --page-all within budget; verify fresh F -> B on dispatch, page boundaries, and publication.
				docsMode.Store(0)
				docsCalls.Store(0)
				beforeFacts := factsCalls.Load()
				respOK := sendRPC(makeDocsSearchReq(100, true))
				if respOK.Error != nil || bytes.Contains(respOK.Result, []byte(`"isError":true`)) {
					t.Fatalf("expected within-budget 2-page docs.search to succeed, got err=%v result=%s", respOK.Error, string(respOK.Result))
				}
				if docsCalls.Load() != 2 {
					t.Fatalf("expected 2 backend page fetches, got %d", docsCalls.Load())
				}
				// Dispatch + 2 page admissions + publication => at least 4 fresh native fact checks.
				if delta := factsCalls.Load() - beforeFacts; delta < 4 {
					t.Fatalf("expected >=4 fresh F->B checks across dispatch, 2 pages, and publication, got %d", delta)
				}

				// (b) Clean page-budget-prefix stop at MaxPages=2 when has_more=true: emits PAGE_BUDGET_PREFIX notice, not error.
				docsMode.Store(1)
				docsCalls.Store(0)
				respPrefix := sendRPC(makeDocsSearchReq(101, true))
				if respPrefix.Error != nil || bytes.Contains(respPrefix.Result, []byte(`"isError":true`)) {
					t.Fatalf("expected clean page-budget-prefix stop, got err=%v result=%s", respPrefix.Error, string(respPrefix.Result))
				}
				if docsCalls.Load() != 2 || !bytes.Contains(respPrefix.Result, []byte(`PAGE_BUDGET_PREFIX`)) || !bytes.Contains(respPrefix.Result, []byte(`c3`)) {
					t.Fatalf("expected PAGE_BUDGET_PREFIX notice after 2 pages with next_cursor=c3, got calls=%d result=%s", docsCalls.Load(), string(respPrefix.Result))
				}

				// (c) Page item count overflow (> MaxPageItems=3) -> explicit RESPONSE_BUDGET_EXCEEDED, no silent truncation.
				docsMode.Store(2)
				docsCalls.Store(0)
				respItems := sendRPC(makeDocsSearchReq(102, true))
				if !bytes.Contains(respItems.Result, []byte(`"isError":true`)) || !bytes.Contains(respItems.Result, []byte(`RESPONSE_BUDGET_EXCEEDED`)) || !bytes.Contains(respItems.Result, []byte(`page_items`)) {
					t.Fatalf("expected RESPONSE_BUDGET_EXCEEDED (page_items), got err=%v result=%s", respItems.Error, string(respItems.Result))
				}

				// (d) Cumulative byte overflow across pages (> MaxCumulativeBytes=1400) -> explicit RESPONSE_BUDGET_EXCEEDED.
				docsMode.Store(3)
				docsCalls.Store(0)
				respCum := sendRPC(makeDocsSearchReq(103, true))
				if !bytes.Contains(respCum.Result, []byte(`"isError":true`)) || !bytes.Contains(respCum.Result, []byte(`RESPONSE_BUDGET_EXCEEDED`)) || !bytes.Contains(respCum.Result, []byte(`cumulative_bytes`)) {
					t.Fatalf("expected RESPONSE_BUDGET_EXCEEDED (cumulative_bytes), got err=%v result=%s", respCum.Error, string(respCum.Result))
				}

				// (e) Single-acquisition byte overflow (> MaxAcquisitionBytes=1200) -> explicit RESPONSE_BUDGET_EXCEEDED.
				docsMode.Store(4)
				docsCalls.Store(0)
				respAcq := sendRPC(makeDocsSearchReq(104, false))
				if !bytes.Contains(respAcq.Result, []byte(`"isError":true`)) || !bytes.Contains(respAcq.Result, []byte(`RESPONSE_BUDGET_EXCEEDED`)) || !bytes.Contains(respAcq.Result, []byte(`acquisition_bytes`)) {
					t.Fatalf("expected RESPONSE_BUDGET_EXCEEDED (acquisition_bytes), got err=%v result=%s", respAcq.Error, string(respAcq.Result))
				}
				if docsCalls.Load() != 1 {
					t.Fatalf("expected exactly 1 backend call on acquisition overflow, got %d", docsCalls.Load())
				}

				// (f) Serialized RPC response overflow (> MaxSerializedRPCBytes=1450) -> explicit RESPONSE_BUDGET_EXCEEDED.
				docsMode.Store(5)
				docsCalls.Store(0)
				respSer := sendRPC(makeDocsSearchReq(105, false))
				if !bytes.Contains(respSer.Result, []byte(`"isError":true`)) || !bytes.Contains(respSer.Result, []byte(`RESPONSE_BUDGET_EXCEEDED`)) || !bytes.Contains(respSer.Result, []byte(`serialized_rpc_bytes`)) {
					t.Fatalf("expected RESPONSE_BUDGET_EXCEEDED (serialized_rpc_bytes), got err=%v result=%s", respSer.Error, string(respSer.Result))
				}

				// (g) Mutating POST (message.send) overflow on acquisition_bytes and serialized_rpc_bytes -> zero retry/replay mutation!
				sendMode.Store(1)
				sendCalls.Store(0)
				respPostAcq := sendRPC(makeMessageSendReq(106))
				if !bytes.Contains(respPostAcq.Result, []byte(`"isError":true`)) || !bytes.Contains(respPostAcq.Result, []byte(`RESPONSE_BUDGET_EXCEEDED`)) {
					t.Fatalf("expected RESPONSE_BUDGET_EXCEEDED on mutating POST acquisition overflow, got %s", string(respPostAcq.Result))
				}
				if sendCalls.Load() != 1 {
					t.Fatalf("mutating POST retried on acquisition overflow: sendCalls=%d", sendCalls.Load())
				}

				sendMode.Store(2)
				sendCalls.Store(0)
				respPostSer := sendRPC(makeMessageSendReq(107))
				if !bytes.Contains(respPostSer.Result, []byte(`"isError":true`)) || !bytes.Contains(respPostSer.Result, []byte(`RESPONSE_BUDGET_EXCEEDED`)) || !bytes.Contains(respPostSer.Result, []byte(`serialized_rpc_bytes`)) {
					t.Fatalf("expected RESPONSE_BUDGET_EXCEEDED (serialized_rpc_bytes) on mutating POST, got %s", string(respPostSer.Result))
				}
				if sendCalls.Load() != 1 {
					t.Fatalf("mutating POST retried on serialized RPC overflow: sendCalls=%d", sendCalls.Load())
				}
			})
		}
	}
}

func TestXIN2794V45EndpointA3164dedRelayAndControlParity(t *testing.T) {
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
	f.backend.Close()
	f.backend = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.backend.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}}
	f.backend.StartTLS()
	defer f.backend.Close()

	f.bundle.Key.Origin = f.backend.URL
	f.bundle.CatalogVersion = pin.Version
	f.bundle.CatalogDigest = pin.Digest
	f.save()

	leafSHA := sha256Sum(serverCert.Certificate[0])
	caSHA := sha256Sum(caPEM)

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
		"relay_ca_sha256":      caSHA,
		"relay_peer_sha256":    leafSHA,
		"relay_cert_sha256":    leafSHA,
		"relay_server_name":    "localhost",
		"response_budgets": map[string]any{
			"max_acquisition_bytes":    2097152,
			"max_page_items":           100,
			"max_pages":                10,
			"max_cumulative_bytes":     4194304,
			"max_serialized_rpc_bytes": 4194304,
		},
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
		t.Fatal("LoadPermissionHostConfig failed:", err)
	}

	// Verify invalid response_budgets in host.json fails closed.
	badCfg := map[string]any{}
	for k, v := range rawCfg {
		badCfg[k] = v
	}
	badCfg["response_budgets"] = map[string]any{"max_acquisition_bytes": 0}
	badBytes, _ := permission.PrivateJSON(badCfg)
	badPath := filepath.Join(dir, "bad-host.json")
	_ = os.WriteFile(badPath, badBytes, 0o600)
	if _, berr := LoadPermissionHostConfig(badPath); berr == nil {
		t.Fatal("expected LoadPermissionHostConfig to reject incomplete/invalid response_budgets")
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
		t.Fatal("control mTLS dial failed:", err)
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

	// Turn 1: stdio open_relay (4-field request) -> exact checkedTurnRelay parity.
	ing1 := request("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "delivery_v45_stdio",
		"stream":             f.header.Delivery,
		"payload_sha256":     f.header.PayloadSHA256,
	})
	ctxID1 := permission.NewID()
	adm1 := request("admit", map[string]any{
		"ingress_id":          ing1["ingress_id"],
		"proposed_context_id": ctxID1,
	})
	man1 := map[string]any{
		"context_id":         ctxID1,
		"context_generation": adm1["generation"],
		"originals":          []permission.NativeHeader{f.header},
	}
	m1Bytes, _ := permission.PrivateJSON(man1)
	man1["bytes"] = len(m1Bytes)
	man1["sha256"] = sha256Sum(m1Bytes)
	request("bind_context", map[string]any{
		"capsule_id": adm1["capsule_id"],
		"manifest":   man1,
	})
	stdioRelay := request("open_relay", map[string]any{
		"capsule_id":   adm1["capsule_id"],
		"recipient_id": loadedCfg.RecipientID,
		"transport":    "stdio",
		"relay_nonce":  permission.NewID(),
	})
	stdioKeys := sortedKeys(stdioRelay)
	if !reflect.DeepEqual(stdioKeys, []string{"executable", "executableSha256", "handle", "transport"}) {
		t.Fatalf("unexpected stdio open_relay keys: %v", stdioKeys)
	}
	handleStr := stdioRelay["handle"].(string)
	ticketBytes, err := base64.RawURLEncoding.Strict().DecodeString(handleStr)
	if err != nil {
		t.Fatal("invalid base64url handle:", err)
	}
	var ticketMap map[string]any
	if json.Unmarshal(ticketBytes, &ticketMap) != nil {
		t.Fatal("invalid JSON ticket:", string(ticketBytes))
	}
	ticketKeys := sortedKeys(ticketMap)
	if !reflect.DeepEqual(ticketKeys, []string{"ca_sha256", "endpoint", "peer_sha256", "token"}) {
		t.Fatalf("expected 4-field pinned stdio ticket [ca_sha256 endpoint peer_sha256 token], got %v (%s)", ticketKeys, string(ticketBytes))
	}
	if ticketMap["ca_sha256"] != caSHA || ticketMap["peer_sha256"] != leafSHA || ticketMap["endpoint"] != loadedCfg.RelayURL {
		t.Fatalf("stdio ticket pin mismatch: got=%v want ca=%s peer=%s url=%s", ticketMap, caSHA, leafSHA, loadedCfg.RelayURL)
	}

	// Verify ServeTurnRelayHandle succeeds with matching SSL_CERT_FILE and fails closed on mismatched ca_sha256 or peer_sha256.
	rawInit, _ := json.Marshal(initializedRequest())
	var initOut bytes.Buffer
	if err := ServeTurnRelayHandle(ctx, handleStr, bytes.NewReader(append(rawInit, '\n')), &initOut); err != nil {
		t.Fatal("ServeTurnRelayHandle failed with valid pins:", err)
	}
	badCAHandle, err := TurnRelayHandleWithTrust(loadedCfg.RelayURL, ticketMap["token"].(string), TurnRelayTrust{
		CASHA256:   strings.Repeat("a", 64),
		PeerSHA256: leafSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	var discard bytes.Buffer
	if err := ServeTurnRelayHandle(ctx, badCAHandle, bytes.NewReader(append(rawInit, '\n')), &discard); err == nil || !strings.Contains(err.Error(), "RELAY_TLS_PIN_MISMATCH") {
		t.Fatalf("expected RELAY_TLS_PIN_MISMATCH for wrong ca_sha256, got %v", err)
	}
	badPeerHandle, err := TurnRelayHandleWithTrust(loadedCfg.RelayURL, ticketMap["token"].(string), TurnRelayTrust{
		CASHA256:   caSHA,
		PeerSHA256: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ServeTurnRelayHandle(ctx, badPeerHandle, bytes.NewReader(append(rawInit, '\n')), &discard); err == nil || !strings.Contains(err.Error(), "RELAY_TLS_PIN_MISMATCH") {
		t.Fatalf("expected RELAY_TLS_PIN_MISMATCH for wrong peer_sha256, got %v", err)
	}
	request("close", map[string]any{"capsule_id": adm1["capsule_id"], "reason": "completed"})

	// Turn 2: http open_relay (4-field request) -> exact checkedTurnRelay parity with ca_sha256 and peer_sha256.
	f.mu.Lock()
	f.header.MessageID = "43"
	f.header.Reference = "header_43"
	f.header.Delivery.Sequence = "2"
	f.mu.Unlock()
	ing2 := request("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "delivery_v45_http",
		"stream":             f.header.Delivery,
		"payload_sha256":     f.header.PayloadSHA256,
	})
	ctxID2 := permission.NewID()
	adm2 := request("admit", map[string]any{
		"ingress_id":          ing2["ingress_id"],
		"proposed_context_id": ctxID2,
	})
	man2 := map[string]any{
		"context_id":         ctxID2,
		"context_generation": adm2["generation"],
		"originals":          []permission.NativeHeader{f.header},
	}
	m2Bytes, _ := permission.PrivateJSON(man2)
	man2["bytes"] = len(m2Bytes)
	man2["sha256"] = sha256Sum(m2Bytes)
	request("bind_context", map[string]any{
		"capsule_id": adm2["capsule_id"],
		"manifest":   man2,
	})
	httpRelay := request("open_relay", map[string]any{
		"capsule_id":   adm2["capsule_id"],
		"recipient_id": loadedCfg.RecipientID,
		"transport":    "http",
		"relay_nonce":  permission.NewID(),
	})
	httpKeys := sortedKeys(httpRelay)
	if !reflect.DeepEqual(httpKeys, []string{"ca_sha256", "handle", "peer_sha256", "transport", "url"}) {
		t.Fatalf("unexpected http open_relay keys: %v", httpKeys)
	}
	if httpRelay["ca_sha256"] != caSHA || httpRelay["peer_sha256"] != leafSHA || httpRelay["url"] != loadedCfg.RelayURL {
		t.Fatalf("http open_relay pin mismatch: %v", httpRelay)
	}
	request("close", map[string]any{"capsule_id": adm2["capsule_id"], "reason": "completed"})
}

func TestXIN2794V45ContractManifestAndResponseBudgetLedger(t *testing.T) {
	manifestRaw, err := os.ReadFile("../../docs/permission/contract-manifest-v6.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		CandidateManifestSHA256 string `json:"candidate_manifest_sha256"`
		CandidateSemanticSHA256 string `json:"candidate_semantic_sha256"`
		Files                   map[string]struct {
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.CandidateManifestSHA256 != "ecc56c2ab0a2c520ab7e5203d2abab06671daf90c28d69f88fe1de8d154109fc" ||
		manifest.CandidateSemanticSHA256 != "15c7ca2bf6df703979df529afbca6828f116b2463b1a818456edfad80bcad799" {
		t.Fatalf("unexpected candidate digests in contract-manifest-v6.json: %+v", manifest)
	}
	for relPath, entry := range manifest.Files {
		b, rerr := os.ReadFile(filepath.Join("../..", filepath.FromSlash(relPath)))
		if rerr != nil {
			t.Fatalf("missing contract-manifest-v6 file %s: %v", relPath, rerr)
		}
		if len(b) != entry.Bytes || sha256Sum(b) != entry.SHA256 {
			t.Fatalf("contract-manifest-v6 mismatch for %s: got bytes=%d sha=%s, want bytes=%d sha=%s", relPath, len(b), sha256Sum(b), entry.Bytes, entry.SHA256)
		}
	}

	ledgerRaw, err := os.ReadFile("../../docs/permission/response-budget-ledger-v6.json")
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct {
		Candidates map[string]struct {
			ResponseBudgets permission.ResponseBudgets `json:"response_budgets"`
		} `json:"candidate_recommendations_for_owner_approval"`
	}
	if err := json.Unmarshal(ledgerRaw, &ledger); err != nil {
		t.Fatal(err)
	}
	if len(ledger.Candidates) < 2 {
		t.Fatalf("expected >=2 candidate budget profiles in ledger, got %d", len(ledger.Candidates))
	}
	for name, cand := range ledger.Candidates {
		if !cand.ResponseBudgets.Valid() {
			t.Fatalf("candidate profile %s has invalid ResponseBudgets: %+v", name, cand.ResponseBudgets)
		}
	}
}

