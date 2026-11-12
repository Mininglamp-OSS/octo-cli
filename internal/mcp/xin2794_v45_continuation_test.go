package mcp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	if !reflect.DeepEqual(ticketKeys, []string{"ca_sha256", "endpoint", "max_response_bytes", "peer_sha256", "server_name", "token"}) {
		t.Fatalf("expected pinned stdio ticket [ca_sha256 endpoint max_response_bytes peer_sha256 server_name token], got %v (%s)", ticketKeys, string(ticketBytes))
	}
	if ticketMap["ca_sha256"] != caSHA || ticketMap["peer_sha256"] != leafSHA || ticketMap["endpoint"] != loadedCfg.RelayURL || ticketMap["server_name"] != "localhost" || ticketMap["max_response_bytes"] != float64(2097152) {
		t.Fatalf("stdio ticket pin/budget mismatch: got=%v want ca=%s peer=%s url=%s sni=localhost max_response_bytes=2097152", ticketMap, caSHA, leafSHA, loadedCfg.RelayURL)
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

	// Turn 3: stdio open_relay with include_tls_trust=true -> exact d007962a checkedTurnRelay & checkTurnRelayTrust parity.
	f.mu.Lock()
	f.header.MessageID = "44"
	f.header.Reference = "header_44"
	f.header.Delivery.Sequence = "3"
	f.mu.Unlock()
	ing3 := request("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "delivery_v49_stdio_tls",
		"stream":             f.header.Delivery,
		"payload_sha256":     f.header.PayloadSHA256,
	})
	ctxID3 := permission.NewID()
	adm3 := request("admit", map[string]any{
		"ingress_id":          ing3["ingress_id"],
		"proposed_context_id": ctxID3,
	})
	man3 := map[string]any{
		"context_id":         ctxID3,
		"context_generation": adm3["generation"],
		"originals":          []permission.NativeHeader{f.header},
	}
	m3Bytes, _ := permission.PrivateJSON(man3)
	man3["bytes"] = len(m3Bytes)
	man3["sha256"] = sha256Sum(m3Bytes)
	request("bind_context", map[string]any{
		"capsule_id": adm3["capsule_id"],
		"manifest":   man3,
	})
	stdioTLSRelay := request("open_relay", map[string]any{
		"capsule_id":        adm3["capsule_id"],
		"recipient_id":      loadedCfg.RecipientID,
		"transport":         "stdio",
		"relay_nonce":       permission.NewID(),
		"include_tls_trust": true,
	})
	if !reflect.DeepEqual(sortedKeys(stdioTLSRelay), []string{"executable", "executableSha256", "handle", "tls", "transport"}) {
		t.Fatalf("unexpected stdio include_tls_trust keys: %v", sortedKeys(stdioTLSRelay))
	}
	tlsSub, ok := stdioTLSRelay["tls"].(map[string]any)
	if !ok || !reflect.DeepEqual(sortedKeys(tlsSub), []string{"ca_file", "ca_sha256", "cert_sha256", "peer_sha256", "server_name"}) {
		t.Fatalf("expected checkTurnRelayTrust keys [ca_file ca_sha256 cert_sha256 peer_sha256 server_name], got %v", stdioTLSRelay["tls"])
	}
	if tlsSub["ca_file"] != loadedCfg.RelayCA || tlsSub["ca_sha256"] != caSHA || tlsSub["cert_sha256"] != leafSHA || tlsSub["peer_sha256"] != leafSHA || tlsSub["server_name"] != "localhost" {
		t.Fatalf("stdio tls sub-object mismatch: %v", tlsSub)
	}
	stdioTLSHandle := stdioTLSRelay["handle"].(string)
	stdioTLSTicketRaw, err := base64.RawURLEncoding.Strict().DecodeString(stdioTLSHandle)
	if err != nil {
		t.Fatal(err)
	}
	var stdioTLSTicket map[string]any
	if json.Unmarshal(stdioTLSTicketRaw, &stdioTLSTicket) != nil {
		t.Fatal("invalid JSON ticket:", string(stdioTLSTicketRaw))
	}
	if !reflect.DeepEqual(sortedKeys(stdioTLSTicket), []string{"ca_sha256", "endpoint", "max_response_bytes", "peer_sha256", "server_name", "token"}) {
		t.Fatalf("expected sandbox-usable stdio ticket [ca_sha256 endpoint max_response_bytes peer_sha256 server_name token], got %v", sortedKeys(stdioTLSTicket))
	}
	if stdioTLSTicket["ca_sha256"] != caSHA || stdioTLSTicket["peer_sha256"] != leafSHA || stdioTLSTicket["server_name"] != "localhost" || stdioTLSTicket["max_response_bytes"] != float64(2097152) {
		t.Fatalf("stdio include_tls_trust ticket mismatch: %v", stdioTLSTicket)
	}
	var initOut3 bytes.Buffer
	if err := ServeTurnRelayHandle(ctx, stdioTLSHandle, bytes.NewReader(append(rawInit, '\n')), &initOut3); err != nil {
		t.Fatal("ServeTurnRelayHandle failed with stdio include_tls_trust ticket:", err)
	}
	// Verify legacy ca_file + cert_sha256 ticket format remains accepted when constructed explicitly.
	legacyHandle, err := TurnRelayHandleWithTrust(loadedCfg.RelayURL, stdioTLSTicket["token"].(string), TurnRelayTrust{
		CAFile:     loadedCfg.RelayCA,
		CertSHA256: leafSHA,
		ServerName: "localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	var initOutLegacy bytes.Buffer
	if err := ServeTurnRelayHandle(ctx, legacyHandle, bytes.NewReader(append(rawInit, '\n')), &initOutLegacy); err != nil {
		t.Fatal("ServeTurnRelayHandle failed with legacy ca_file+cert_sha256 ticket:", err)
	}
	request("close", map[string]any{"capsule_id": adm3["capsule_id"], "reason": "completed"})

	// Turn 4: http open_relay with include_tls_trust=true -> exact checkedTurnRelay parity.
	f.mu.Lock()
	f.header.MessageID = "45"
	f.header.Reference = "header_45"
	f.header.Delivery.Sequence = "4"
	f.mu.Unlock()
	ing4 := request("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "delivery_v49_http_tls",
		"stream":             f.header.Delivery,
		"payload_sha256":     f.header.PayloadSHA256,
	})
	ctxID4 := permission.NewID()
	adm4 := request("admit", map[string]any{
		"ingress_id":          ing4["ingress_id"],
		"proposed_context_id": ctxID4,
	})
	man4 := map[string]any{
		"context_id":         ctxID4,
		"context_generation": adm4["generation"],
		"originals":          []permission.NativeHeader{f.header},
	}
	m4Bytes, _ := permission.PrivateJSON(man4)
	man4["bytes"] = len(m4Bytes)
	man4["sha256"] = sha256Sum(m4Bytes)
	request("bind_context", map[string]any{
		"capsule_id": adm4["capsule_id"],
		"manifest":   man4,
	})
	httpTLSRelay := request("open_relay", map[string]any{
		"capsule_id":        adm4["capsule_id"],
		"recipient_id":      loadedCfg.RecipientID,
		"transport":         "http",
		"relay_nonce":       permission.NewID(),
		"include_tls_trust": true,
	})
	if !reflect.DeepEqual(sortedKeys(httpTLSRelay), []string{"ca_sha256", "handle", "peer_sha256", "tls", "transport", "url"}) {
		t.Fatalf("unexpected http include_tls_trust keys: %v", sortedKeys(httpTLSRelay))
	}
	httpTLSSub, ok := httpTLSRelay["tls"].(map[string]any)
	if !ok || !reflect.DeepEqual(sortedKeys(httpTLSSub), []string{"ca_file", "ca_sha256", "cert_sha256", "peer_sha256", "server_name"}) {
		t.Fatalf("expected checkTurnRelayTrust keys on http open_relay, got %v", httpTLSRelay["tls"])
	}
	request("close", map[string]any{"capsule_id": adm4["capsule_id"], "reason": "completed"})
}

type countingReadCloser struct {
	r     io.Reader
	bytes *atomic.Int64
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.bytes.Add(int64(n))
	}
	return n, err
}

func (c *countingReadCloser) Close() error { return nil }

func TestXIN2794V49ReviewFindingsTurnRelayBinaryAndRPCEnvelopeBudgets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Finding 3 (part A): ResponseBudgets.Valid() rejects MaxSerializedRPCBytes < MinSerializedRPCBudgetBytes (1024)
	// and rejects MaxSerializedRPCBytes < MaxAcquisitionBytes.
	for _, invalid := range []permission.ResponseBudgets{
		{MaxAcquisitionBytes: 512, MaxPageItems: 10, MaxPages: 2, MaxCumulativeBytes: 1024, MaxSerializedRPCBytes: 1},
		{MaxAcquisitionBytes: 512, MaxPageItems: 10, MaxPages: 2, MaxCumulativeBytes: 1024, MaxSerializedRPCBytes: 1023},
		{MaxAcquisitionBytes: 2048, MaxPageItems: 10, MaxPages: 2, MaxCumulativeBytes: 4096, MaxSerializedRPCBytes: 1024},
	} {
		if invalid.Valid() {
			t.Fatalf("expected invalid ResponseBudgets to fail Valid(): %+v", invalid)
		}
	}
	minValid := permission.ResponseBudgets{
		MaxAcquisitionBytes:   512,
		MaxPageItems:          10,
		MaxPages:              2,
		MaxCumulativeBytes:    1024,
		MaxSerializedRPCBytes: permission.MinSerializedRPCBudgetBytes,
	}
	if !minValid.Valid() {
		t.Fatalf("expected boundary ResponseBudgets at MinSerializedRPCBudgetBytes (%d) to be valid", permission.MinSerializedRPCBudgetBytes)
	}

	// Finding 1: ServeTurnRelay enforces MaxAcquisitionBytes via Content-Length pre-check (0 body bytes read)
	// and via io.LimitReader(limit+1) on chunked/streaming bodies (at most limit+1 bytes read), failing closed
	// with RESPONSE_BUDGET_EXCEEDED and writing no truncated output.
	var relayMode atomic.Int32
	var serverWrites atomic.Int64
	relaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch relayMode.Load() {
		case 0:
			// Oversized declared Content-Length (4096 > 512).
			payload := `{"jsonrpc":"2.0","id":1,"result":{"pad":"` + strings.Repeat("a", 4096) + `"}}`
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			n, _ := w.Write([]byte(payload))
			serverWrites.Store(int64(n))
		case 1:
			// Chunked/streaming response without Content-Length exceeding 512 bytes.
			flusher, _ := w.(http.Flusher)
			chunk := []byte(`{"jsonrpc":"2.0","id":1,"result":{"pad":"` + strings.Repeat("b", 1024) + `"}}`)
			n, _ := w.Write(chunk)
			serverWrites.Store(int64(n))
			if flusher != nil {
				flusher.Flush()
			}
		case 2:
			// Within 512-byte acquisition budget.
			okResp := `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`
			_, _ = w.Write([]byte(okResp))
		}
	}))
	defer relaySrv.Close()

	budgetCtx := permission.WithResponseBudgets(ctx, minValid)
	ticket, _ := json.Marshal(map[string]any{
		"endpoint": relaySrv.URL,
		"token":    strings.Repeat("c", 64),
	})
	reqLine := []byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n")

	// Mode 0: Content-Length > 512 fails closed with ResponseBudgetError and empty output.
	relayMode.Store(0)
	var relayOut bytes.Buffer
	err := ServeTurnRelay(budgetCtx, bytes.NewReader(ticket), bytes.NewReader(reqLine), &relayOut)
	if err == nil || err.Error() != "RESPONSE_BUDGET_EXCEEDED" || relayOut.Len() != 0 {
		t.Fatalf("ServeTurnRelay Content-Length overflow: err=%v outLen=%d", err, relayOut.Len())
	}

	// Also verify ReadBoundedHTTPResponseBody directly against a counting reader:
	// when Content-Length > limit, 0 bytes are read from Body; when streaming without Content-Length,
	// exactly limit+1 bytes are read from a 1 MiB stream.
	var countedCL atomic.Int64
	clResp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Length": []string{"8192"}},
		ContentLength: 8192,
		Body:          &countingReadCloser{r: bytes.NewReader(bytes.Repeat([]byte("x"), 8192)), bytes: &countedCL},
	}
	_, obsCL, overCL, errCL := permission.ReadBoundedHTTPResponseBody(clResp, 512)
	if errCL != nil || !overCL || obsCL != 8192 || countedCL.Load() != 0 {
		t.Fatalf("ReadBoundedHTTPResponseBody Content-Length precheck failed: err=%v over=%v obs=%d read=%d", errCL, overCL, obsCL, countedCL.Load())
	}
	var countedStream atomic.Int64
	streamResp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{},
		ContentLength: -1,
		Body:          &countingReadCloser{r: bytes.NewReader(bytes.Repeat([]byte("y"), 1<<20)), bytes: &countedStream},
	}
	_, obsStream, overStream, errStream := permission.ReadBoundedHTTPResponseBody(streamResp, 512)
	if errStream != nil || !overStream || obsStream != 513 || countedStream.Load() != 513 {
		t.Fatalf("ReadBoundedHTTPResponseBody stream cap failed: err=%v over=%v obs=%d read=%d", errStream, overStream, obsStream, countedStream.Load())
	}

	// Mode 1: Chunked/streaming overflow in ServeTurnRelay fails closed with RESPONSE_BUDGET_EXCEEDED and empty output.
	relayMode.Store(1)
	relayOut.Reset()
	err = ServeTurnRelay(budgetCtx, bytes.NewReader(ticket), bytes.NewReader(reqLine), &relayOut)
	if err == nil || err.Error() != "RESPONSE_BUDGET_EXCEEDED" || relayOut.Len() != 0 {
		t.Fatalf("ServeTurnRelay chunked overflow: err=%v outLen=%d", err, relayOut.Len())
	}

	// Mode 2: Within budget succeeds and writes exact JSON line.
	relayMode.Store(2)
	relayOut.Reset()
	if err := ServeTurnRelay(budgetCtx, bytes.NewReader(ticket), bytes.NewReader(reqLine), &relayOut); err != nil {
		t.Fatalf("ServeTurnRelay within-budget failed: %v", err)
	}
	if strings.TrimSpace(relayOut.String()) != `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}` {
		t.Fatalf("ServeTurnRelay within-budget output mismatch: %q", relayOut.String())
	}

	// Finding 2 & Finding 3 (part B): Managed binary response (BinaryResponse=true, InlineBinaryLimit=1<<20,
	// docs.scene.export) and lower-boundary MaxSerializedRPCBytes (1024 bytes) across HTTP and stdio.
	f := newManagedFixture(t)
	caps := []string{
		"octo-cli/docs/op/docs.scene.export",
		"octo-cli/docs/op/docs.search",
	}
	f.bundle.Capability.Rules[0]["capabilities"] = caps
	f.save()
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
	if err := f.coordinator.ConfigureResponseBudgets(minValid); err != nil {
		t.Fatalf("ConfigureResponseBudgets(minValid) failed: %v", err)
	}

	var exportMode atomic.Int32
	var exportCalls atomic.Int32
	baseServe := f.serve
	f.backend.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/permission-facts/v1":
			baseServe(w, r)
		case "/v1/bot/docs/doc_1/export":
			exportCalls.Add(1)
			w.Header().Set("Content-Type", "image/png")
			switch exportMode.Load() {
			case 0:
				// Binary response within 512-byte MaxAcquisitionBytes (128 bytes).
				_, _ = w.Write(bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 32))
			case 1:
				// Binary response exceeding 512-byte MaxAcquisitionBytes via Content-Length (2048 bytes).
				payload := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 512)
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
				_, _ = w.Write(payload)
			case 2:
				// Binary response exceeding 512-byte MaxAcquisitionBytes via chunked stream (no Content-Length).
				flusher, _ := w.(http.Flusher)
				_, _ = w.Write(bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 512))
				if flusher != nil {
					flusher.Flush()
				}
			}
		case "/v1/bot/docs/search":
			w.Header().Set("Content-Type", "application/json")
			// Within 512-byte MaxAcquisitionBytes (~430 bytes), but with quote escaping exceeds 1024-byte MaxSerializedRPCBytes.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total":      1,
				"items":      []map[string]any{{"docId": "d_bound", "snippet": strings.Repeat(`"`, 180)}},
				"nextCursor": "",
			})
		default:
			baseServe(w, r)
		}
	})

	turn := f.turn()
	defer turn.Close()
	srv := f.server(turn)
	if initResp, _, _ := srv.dispatchManaged(ctx, initializedRequest()); initResp.Error != nil {
		t.Fatalf("initialize failed: %+v", initResp.Error)
	}
	makeDeclaredCall := func(toolName string, args map[string]any) rpcRequest {
		t.Helper()
		params, _ := json.Marshal(map[string]any{
			"name":      toolName,
			"arguments": args,
		})
		req := rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params}
		if err := relayDeclaration(&req, turn.source); err != nil {
			t.Fatal(err)
		}
		return req
	}

	// Binary export within MaxAcquisitionBytes succeeds.
	exportMode.Store(0)
	exportCalls.Store(0)
	okExportReq := makeDeclaredCall("call_op", map[string]any{
		"operation_id": "docs.scene.export",
		"arguments":    map[string]any{"docId": "doc_1", "format": "png"},
	})
	okExportResp, has, _ := srv.dispatchManaged(ctx, okExportReq)
	if !has || okExportResp.Error != nil {
		t.Fatalf("expected within-budget binary export to succeed, got %+v", okExportResp)
	}
	okExportText := string(okExportResp.Result)
	if !strings.Contains(okExportText, `\"inline\": true`) || !strings.Contains(okExportText, `content_base64`) || exportCalls.Load() != 1 {
		t.Fatalf("unexpected within-budget binary export payload: %s (calls=%d)", okExportText, exportCalls.Load())
	}

	// Binary export exceeding MaxAcquisitionBytes fails closed with RESPONSE_BUDGET_EXCEEDED (single attempt, no retry)
	// for both Content-Length (mode 1) and chunked stream (mode 2), across call_op and execute.
	for _, mode := range []int32{1, 2} {
		for _, toolName := range []string{"call_op", "execute"} {
			exportMode.Store(mode)
			before := exportCalls.Load()
			req := makeDeclaredCall(toolName, map[string]any{
				"operation_id": "docs.scene.export",
				"arguments":    map[string]any{"docId": "doc_1", "format": "png"},
			})
			resp, hasResp, _ := srv.dispatchManaged(ctx, req)
			if !hasResp || resp.Error != nil {
				t.Fatalf("mode=%d tool=%s unexpected RPC error: %+v", mode, toolName, resp)
			}
			if gotCalls := exportCalls.Load() - before; gotCalls != 1 {
				t.Fatalf("mode=%d tool=%s expected single attempt without retry, got %d calls", mode, toolName, gotCalls)
			}
			text := string(resp.Result)
			if !strings.Contains(text, `RESPONSE_BUDGET_EXCEEDED`) || !strings.Contains(text, `acquisition_bytes`) {
				t.Fatalf("mode=%d tool=%s expected RESPONSE_BUDGET_EXCEEDED acquisition_bytes, got: %s", mode, toolName, text)
			}
		}
	}

	// Also verify StreamBoundedHTTPResponseBody directly against a counting reader:
	// Content-Length > acquisitionLimit reads 0 bytes; streaming > acquisitionLimit reads acquisitionLimit+1 bytes.
	var binCLCount atomic.Int64
	binCLResp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Length": []string{"4096"}},
		ContentLength: 4096,
		Body:          &countingReadCloser{r: bytes.NewReader(bytes.Repeat([]byte("z"), 4096)), bytes: &binCLCount},
	}
	_, binCLSize, _, binCLOver, binCLErr := permission.StreamBoundedHTTPResponseBody(binCLResp, 1<<20, 512)
	if binCLErr != nil || !binCLOver || binCLSize != 4096 || binCLCount.Load() != 0 {
		t.Fatalf("StreamBoundedHTTPResponseBody Content-Length check failed: err=%v over=%v size=%d read=%d", binCLErr, binCLOver, binCLSize, binCLCount.Load())
	}
	var binStreamCount atomic.Int64
	binStreamResp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{},
		ContentLength: -1,
		Body:          &countingReadCloser{r: bytes.NewReader(bytes.Repeat([]byte("z"), 1<<20)), bytes: &binStreamCount},
	}
	_, binStreamSize, _, binStreamOver, binStreamErr := permission.StreamBoundedHTTPResponseBody(binStreamResp, 1<<20, 512)
	if binStreamErr != nil || !binStreamOver || binStreamSize != 513 || binStreamCount.Load() != 513 {
		t.Fatalf("StreamBoundedHTTPResponseBody stream cap failed: err=%v over=%v size=%d read=%d", binStreamErr, binStreamOver, binStreamSize, binStreamCount.Load())
	}

	// Finding 3 (part B): At the minimum valid MaxSerializedRPCBytes (1024), even with a long request ID,
	// enforceSerializedRPCBudget produces a valid RESPONSE_BUDGET_EXCEEDED envelope whose serialized length is <= 1024.
	for _, toolName := range []string{"call_op", "execute"} {
		req := makeDeclaredCall(toolName, map[string]any{
			"operation_id": "docs.search",
			"arguments":    map[string]any{"q": "bound"},
		})
		req.ID = json.RawMessage(`"` + strings.Repeat("id_", 60) + `"`)
		resp, hasResp, _ := srv.dispatchManaged(ctx, req)
		if !hasResp {
			t.Fatalf("tool=%s expected response", toolName)
		}
		var enc bytes.Buffer
		if err := json.NewEncoder(&enc).Encode(resp); err != nil {
			t.Fatal(err)
		}
		if int64(enc.Len()) > permission.MinSerializedRPCBudgetBytes {
			t.Fatalf("tool=%s replacement RESPONSE_BUDGET_EXCEEDED envelope (%d bytes) exceeded MinSerializedRPCBudgetBytes (%d): %s", toolName, enc.Len(), permission.MinSerializedRPCBudgetBytes, enc.String())
		}
		if !strings.Contains(enc.String(), "RESPONSE_BUDGET_EXCEEDED") || !strings.Contains(enc.String(), "serialized_rpc_bytes") {
			t.Fatalf("tool=%s replacement envelope missing RESPONSE_BUDGET_EXCEEDED / serialized_rpc_bytes: %s", toolName, enc.String())
		}
	}
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

func makeCustomDNSCertificates(t *testing.T, dnsName string) (server, client tls.Certificate, roots *x509.CertPool, caPEM []byte, caSHA, leafSHA, clientSHA string) {
	t.Helper()
	_, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(101),
		Subject:               pkix.Name{CommonName: "v52-ca-" + dnsName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	roots.AddCert(ca)
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caSHA = sha256Sum(caPEM)

	makeLeaf := func(serial int64, usage x509.ExtKeyUsage) tls.Certificate {
		pub, key, gerr := ed25519.GenerateKey(rand.Reader)
		if gerr != nil {
			t.Fatal(gerr)
		}
		// Intentionally omit IPAddresses so connecting to https://127.0.0.1:<port>
		// REQUIRES server_name (SNI/SAN override) to be preserved in the relay ticket.
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: dnsName},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			DNSNames:     []string{dnsName},
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}
		der, cerr := x509.CreateCertificate(rand.Reader, tmpl, ca, pub, caKey)
		if cerr != nil {
			t.Fatal(cerr)
		}
		return tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}
	}
	server = makeLeaf(102, x509.ExtKeyUsageServerAuth)
	client = makeLeaf(103, x509.ExtKeyUsageClientAuth)
	leafSHA = sha256Sum(server.Certificate[0])
	clientSHA = sha256Sum(client.Certificate[0])
	return server, client, roots, caPEM, caSHA, leafSHA, clientSHA
}

// TestXIN2794V52CrossProcessTurnRelayBudgetsAndTLSPinning verifies the v52 [C-B1] and [C-B2]
// repairs end-to-end across a real child octo-cli process (`octo-cli --turn-relay <handle>`):
//   - [C-B1]: writeManagedHTTP emits X-Octo-Acquisition-Budget-Bytes on hijacked responses and
//     open_relay embeds max_response_bytes in the stdio ticket so cross-process octo --turn-relay
//     enforces configured MaxAcquisitionBytes (rather than falling back to 64 MiB).
//   - [C-B2]: open_relay preserves server_name alongside ca_sha256 and peer_sha256 (for both
//     include_tls_trust=false and include_tls_trust=true) without replacing CA/leaf pins with a
//     host-private ca_file path, while positive/negative CA, SNI/SAN, and leaf pins and the
//     denied-write gate behave fail-closed across the actual child CLI binary.
func TestXIN2794V52CrossProcessTurnRelayBudgetsAndTLSPinning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	dir := secureTestDir(t)
	cliBin := filepath.Join(dir, "octo-cli")
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", cliBin, "github.com/Mininglamp-OSS/octo-cli/cmd/octo-cli")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build octo-cli failed: %v\n%s", err, out)
	}
	if err := os.Chmod(cliBin, 0o700); err != nil {
		t.Fatal(err)
	}

	const relayDNSName = "relay.octo.internal"
	serverCert, clientCert, roots, caPEM, caSHA, leafSHA, peerHash := makeCustomDNSCertificates(t, relayDNSName)
	_, _, _, wrongCAPEM, _, _, _ := makeCustomDNSCertificates(t, relayDNSName)

	// Host-private CA path exists only during NewPermissionHost initialization, then is deleted
	// to prove child stdio relay workers inside a sandbox do not depend on host-private ca_file paths.
	hostPrivateCADir := filepath.Join(dir, "host-only-ca")
	if err := os.MkdirAll(hostPrivateCADir, 0o700); err != nil {
		t.Fatal(err)
	}
	hostPrivateCAPath := filepath.Join(hostPrivateCADir, "ca.pem")
	if err := os.WriteFile(hostPrivateCAPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	sandboxCAPath := filepath.Join(dir, "sandbox-ca.pem")
	if err := os.WriteFile(sandboxCAPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	wrongCAPath := filepath.Join(dir, "wrong-ca.pem")
	if err := os.WriteFile(wrongCAPath, wrongCAPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	f := newManagedFixture(t)
	// Grant only docs.search (read) so docs.create / message.send are denied at the write gate.
	caps := []string{"octo-cli/docs/op/docs.search"}
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
	var writeDispatches atomic.Int32
	baseServe := f.serve
	f.backend.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/docs/search":
			docsCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			switch docsMode.Load() {
			case 0:
				// Within budget.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total": 1,
					"items": []map[string]any{{"docId": "doc-ok", "title": "v52"}},
				})
			case 1:
				// Over MaxAcquisitionBytes (2048) with Content-Length.
				body := `{"total":1,"items":[{"docId":"` + strings.Repeat("a", 4096) + `"}]}`
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
				_, _ = w.Write([]byte(body))
			case 2:
				// Over MaxAcquisitionBytes (2048) via chunked streaming.
				flusher, _ := w.(http.Flusher)
				_, _ = w.Write([]byte(`{"total":1,"items":[{"docId":"`))
				if flusher != nil {
					flusher.Flush()
				}
				_, _ = w.Write([]byte(strings.Repeat("b", 4096) + `"}]}`))
				if flusher != nil {
					flusher.Flush()
				}
			case 3:
				// Within MaxAcquisitionBytes (<= 2048) but wrapped MCP JSON-RPC envelope > MaxSerializedRPCBytes (2048).
				body := `{"total":1,"items":[{"docId":"` + strings.Repeat("c", 1900) + `"}]}`
				_, _ = w.Write([]byte(body))
			}
		case "/v1/bot/docs/create":
			writeDispatches.Add(1)
			http.Error(w, "unexpected write dispatch", http.StatusInternalServerError)
		default:
			baseServe(w, r)
		}
	})

	relayRaw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer relayRaw.Close()
	relayTLS := permission.PublicationTLSListener(relayRaw, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
	})
	relayURL := "https://" + relayRaw.Addr().String() + "/mcp"

	const configuredAcqBudget = int64(2048)
	const configuredSerBudget = int64(2048)
	budgets := &permission.ResponseBudgets{
		MaxAcquisitionBytes:   configuredAcqBudget,
		MaxPageItems:          10,
		MaxPages:              3,
		MaxCumulativeBytes:    4096,
		MaxSerializedRPCBytes: configuredSerBudget,
	}
	cfg := PermissionHostConfig{
		Version:             1,
		Origin:              f.backend.URL,
		BotUID:              "bot",
		RecipientID:         "endpoint-1",
		EndpointIncarnation: strings.Repeat("5", 32),
		PeerSHA256:          peerHash,
		RelayURL:            relayURL,
		RelayExecutable:     cliBin,
		RelayCA:             hostPrivateCAPath,
		RelayCASHA256:       caSHA,
		RelayPeerSHA256:     leafSHA,
		RelayCertSHA256:     leafSHA,
		RelayServerName:     relayDNSName,
		ResponseBudgets:     budgets,
	}
	host, err := NewPermissionHost(cfg, f.coordinator, f.policies, f.reader, f.server)
	if err != nil {
		t.Fatal("NewPermissionHost failed:", err)
	}
	// Remove the host-private CA file now that the host has started.
	if err := os.RemoveAll(hostPrivateCADir); err != nil {
		t.Fatal(err)
	}

	relayServer := &http.Server{Handler: host, ReadHeaderTimeout: 5 * time.Second}
	defer relayServer.Close()
	go func() { _ = relayServer.Serve(relayTLS) }()

	controlListener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    roots,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controlListener.Close()
	controlDone := make(chan error, 1)
	go func() {
		c, aerr := controlListener.Accept()
		if aerr != nil {
			controlDone <- aerr
			return
		}
		controlDone <- host.ServeControl(ctx, c.(*tls.Conn))
	}()
	controlConn, err := tls.Dial("tcp", controlListener.Addr().String(), &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   relayDNSName,
		Certificates: []tls.Certificate{clientCert},
	})
	if err != nil {
		t.Fatal("control dial failed:", err)
	}
	defer func() {
		_ = controlConn.Close()
		<-controlDone
	}()

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
		_ = controlConn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, werr := controlConn.Write(append(prefix[:], frame...)); werr != nil {
			t.Fatal(werr)
		}
		if _, rerr := io.ReadFull(controlConn, prefix[:]); rerr != nil {
			t.Fatal(rerr)
		}
		body := make([]byte, binary.BigEndian.Uint32(prefix[:]))
		if _, rerr := io.ReadFull(controlConn, body); rerr != nil {
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
		"endpoint_incarnation": cfg.EndpointIncarnation,
		"configured_origin":    cfg.Origin,
		"recipient_id":         cfg.RecipientID,
	})

	openTurn := func(seq string, includeTLS bool) (capsuleID string, reply map[string]any, ticket map[string]any) {
		t.Helper()
		f.mu.Lock()
		f.header.MessageID = "52" + seq
		f.header.Reference = "header_v52_" + seq
		f.header.Delivery.Sequence = seq
		f.mu.Unlock()
		ing := request("register_ingress", map[string]any{
			"key":                f.bundle.Key,
			"delivery_reference": "delivery_v52_" + seq,
			"stream":             f.header.Delivery,
			"payload_sha256":     f.header.PayloadSHA256,
		})
		ctxID := permission.NewID()
		adm := request("admit", map[string]any{
			"ingress_id":          ing["ingress_id"],
			"proposed_context_id": ctxID,
		})
		man := map[string]any{
			"context_id":         ctxID,
			"context_generation": adm["generation"],
			"originals":          []permission.NativeHeader{f.header},
		}
		mBytes, _ := permission.PrivateJSON(man)
		man["bytes"] = len(mBytes)
		man["sha256"] = sha256Sum(mBytes)
		request("bind_context", map[string]any{
			"capsule_id": adm["capsule_id"],
			"manifest":   man,
		})
		openPayload := map[string]any{
			"capsule_id":   adm["capsule_id"],
			"recipient_id": cfg.RecipientID,
			"transport":    "stdio",
			"relay_nonce":  permission.NewID(),
		}
		if includeTLS {
			openPayload["include_tls_trust"] = true
		}
		reply = request("open_relay", openPayload)
		handleStr := reply["handle"].(string)
		rawTicket, derr := base64.RawURLEncoding.Strict().DecodeString(handleStr)
		if derr != nil {
			t.Fatal(derr)
		}
		if json.Unmarshal(rawTicket, &ticket) != nil {
			t.Fatalf("invalid ticket JSON: %s", string(rawTicket))
		}
		return adm["capsule_id"].(string), reply, ticket
	}

	runChildTurnRelay := func(handle, sslCertFile string, lines ...[]byte) (stdout, stderr string, err error) {
		t.Helper()
		var input bytes.Buffer
		for _, l := range lines {
			input.Write(l)
			input.WriteByte('\n')
		}
		cmd := exec.CommandContext(ctx, cliBin, "--turn-relay", handle)
		env := []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + dir,
			"TMPDIR=" + dir,
		}
		if sslCertFile != "" {
			env = append(env, "SSL_CERT_FILE="+sslCertFile)
		}
		cmd.Env = env
		cmd.Stdin = &input
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		err = cmd.Run()
		return outBuf.String(), errBuf.String(), err
	}

	makeCallLine := func(seq, id string, opID string, args map[string]any) []byte {
		t.Helper()
		params, _ := json.Marshal(map[string]any{
			"name": "call_op",
			"arguments": map[string]any{
				"operation_id": opID,
				"arguments":    args,
			},
		})
		req := rpcRequest{
			Jsonrpc: "2.0",
			ID:      json.RawMessage(fmt.Sprintf("%q", id)),
			Method:  "tools/call",
			Params:  params,
		}
		if err := relayDeclaration(&req, permission.DeclarationV2{
			Sender:      "alice",
			Channel:     "group",
			ChannelType: 2,
			SpaceID:     "space",
			MessageID:   "52" + seq,
		}); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(req)
		return b
	}
	initLine, _ := json.Marshal(initializedRequest())

	// 1. Verify [C-B1] & [C-B2] on open_relay (both include_tls_trust=false and include_tls_trust=true).
	for idx, includeTLS := range []bool{false, true} {
		seq := fmt.Sprintf("%d", idx+1)
		capID, reply, ticket := openTurn(seq, includeTLS)
		handle := reply["handle"].(string)

		wantKeys := []string{"ca_sha256", "endpoint", "max_response_bytes", "peer_sha256", "server_name", "token"}
		if !reflect.DeepEqual(sortedKeys(ticket), wantKeys) {
			t.Fatalf("includeTLS=%v unexpected ticket keys: got %v want %v", includeTLS, sortedKeys(ticket), wantKeys)
		}
		if ticket["ca_sha256"] != caSHA || ticket["peer_sha256"] != leafSHA || ticket["server_name"] != relayDNSName || ticket["max_response_bytes"] != float64(configuredAcqBudget) {
			t.Fatalf("includeTLS=%v ticket mismatch: %v", includeTLS, ticket)
		}
		if includeTLS {
			tlsSub, ok := reply["tls"].(map[string]any)
			if !ok || !reflect.DeepEqual(sortedKeys(tlsSub), []string{"ca_file", "ca_sha256", "cert_sha256", "peer_sha256", "server_name"}) {
				t.Fatalf("includeTLS=true unexpected tls keys: %v", reply["tls"])
			}
		}

		// Verify raw hijacked HTTP response from writeManagedHTTP carries X-Octo-Acquisition-Budget-Bytes.
		httpClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS13,
					RootCAs:    roots,
					ServerName: relayDNSName,
				},
			},
		}
		docsMode.Store(0)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL, bytes.NewReader(makeCallLine(seq, "http_hdr", "docs.search", map[string]any{"q": "hdr"})))
		if err != nil {
			t.Fatal(err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+ticket["token"].(string))
		httpReq.Header.Set("Content-Type", "application/json")
		httpResp, err := httpClient.Do(httpReq)
		if err != nil {
			t.Fatalf("direct hijacked HTTP call failed: %v", err)
		}
		respBody, _ := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()
		if gotHdr := httpResp.Header.Get("X-Octo-Acquisition-Budget-Bytes"); gotHdr != "2048" {
			t.Fatalf("writeManagedHTTP hijacked response missing X-Octo-Acquisition-Budget-Bytes=2048: got=%q headers=%v body=%s", gotHdr, httpResp.Header, string(respBody))
		}

		// Positive child-process execution (with hostPrivateCAPath deleted, using sandbox SSL_CERT_FILE).
		docsMode.Store(0)
		stdout, stderr, err := runChildTurnRelay(
			handle,
			sandboxCAPath,
			initLine,
			makeCallLine(seq, "pos_ok", "docs.search", map[string]any{"q": "v52"}),
		)
		if err != nil {
			t.Fatalf("includeTLS=%v child octo-cli --turn-relay failed: err=%v stderr=%s stdout=%s", includeTLS, err, stderr, stdout)
		}
		if !strings.Contains(stdout, "doc-ok") {
			t.Fatalf("includeTLS=%v expected doc-ok in child stdout, got: %s", includeTLS, stdout)
		}

		// Over-budget upstream acquisition (Content-Length & streaming) -> replacement envelope relayed cleanly.
		for _, mode := range []int32{1, 2} {
			docsMode.Store(mode)
			outAcq, errAcqOut, errAcq := runChildTurnRelay(
				handle,
				sandboxCAPath,
				initLine,
				makeCallLine(seq, fmt.Sprintf("acq_%d", mode), "docs.search", map[string]any{"q": "over"}),
			)
			if errAcq != nil {
				t.Fatalf("mode=%d child octo-cli --turn-relay failed on replacement envelope: err=%v stderr=%s", mode, errAcq, errAcqOut)
			}
			if !strings.Contains(outAcq, "RESPONSE_BUDGET_EXCEEDED") || !strings.Contains(outAcq, "acquisition_bytes") {
				t.Fatalf("mode=%d expected RESPONSE_BUDGET_EXCEEDED (acquisition_bytes) in child stdout, got: %s", mode, outAcq)
			}
		}

		// Over-budget serialized RPC envelope -> replacement envelope relayed cleanly.
		docsMode.Store(3)
		outSer, errSerOut, errSer := runChildTurnRelay(
			handle,
			sandboxCAPath,
			initLine,
			makeCallLine(seq, "ser_over", "docs.search", map[string]any{"q": "ser"}),
		)
		if errSer != nil {
			t.Fatalf("child octo-cli --turn-relay failed on serialized_rpc_bytes replacement: err=%v stderr=%s", errSer, errSerOut)
		}
		if !strings.Contains(outSer, "RESPONSE_BUDGET_EXCEEDED") || !strings.Contains(outSer, "serialized_rpc_bytes") {
			t.Fatalf("expected RESPONSE_BUDGET_EXCEEDED (serialized_rpc_bytes) in child stdout, got: %s", outSer)
		}

		// Denied-write gate across child octo-cli --turn-relay -> -32007 capability_denied, zero backend write dispatches.
		writeDispatches.Store(0)
		outDenied, errDeniedOut, errDenied := runChildTurnRelay(
			handle,
			sandboxCAPath,
			initLine,
			makeCallLine(seq, "deny_write", "docs.create", map[string]any{"title": "forbidden"}),
		)
		if errDenied != nil {
			t.Fatalf("child octo-cli --turn-relay failed on denied write: err=%v stderr=%s", errDenied, errDeniedOut)
		}
		if !strings.Contains(outDenied, "-32007") || !strings.Contains(outDenied, "capability_denied") {
			t.Fatalf("expected -32007 capability_denied on denied write, got: %s", outDenied)
		}
		if writeDispatches.Load() != 0 {
			t.Fatalf("expected 0 backend dispatches on denied write, got %d", writeDispatches.Load())
		}

		// Negative TLS refusals across child octo-cli --turn-relay process.
		// (a) Wrong CA in SSL_CERT_FILE -> RELAY_TLS_PIN_MISMATCH.
		if out, errOut, cerr := runChildTurnRelay(handle, wrongCAPath, initLine); cerr == nil || out != "" || !strings.Contains(errOut, "RELAY_TLS_PIN_MISMATCH") {
			t.Fatalf("expected RELAY_TLS_PIN_MISMATCH with wrong SSL_CERT_FILE: err=%v stdout=%q stderr=%q", cerr, out, errOut)
		}
		// (b) Wrong ca_sha256 in ticket -> RELAY_TLS_PIN_MISMATCH.
		badCAHandle, err := TurnRelayHandleWithTrust(relayURL, ticket["token"].(string), TurnRelayTrust{
			CASHA256:         strings.Repeat("a", 64),
			PeerSHA256:       leafSHA,
			ServerName:       relayDNSName,
			MaxResponseBytes: configuredAcqBudget,
		})
		if err != nil {
			t.Fatal(err)
		}
		if out, errOut, cerr := runChildTurnRelay(badCAHandle, sandboxCAPath, initLine); cerr == nil || out != "" || !strings.Contains(errOut, "RELAY_TLS_PIN_MISMATCH") {
			t.Fatalf("expected RELAY_TLS_PIN_MISMATCH with wrong ca_sha256: err=%v stdout=%q stderr=%q", cerr, out, errOut)
		}
		// (c) Wrong server_name in ticket -> RELAY_UNAVAILABLE.
		badSNIHandle, err := TurnRelayHandleWithTrust(relayURL, ticket["token"].(string), TurnRelayTrust{
			CASHA256:         caSHA,
			PeerSHA256:       leafSHA,
			ServerName:       "wrong.octo.internal",
			MaxResponseBytes: configuredAcqBudget,
		})
		if err != nil {
			t.Fatal(err)
		}
		if out, errOut, cerr := runChildTurnRelay(badSNIHandle, sandboxCAPath, initLine); cerr == nil || out != "" || !strings.Contains(errOut, "RELAY_UNAVAILABLE") {
			t.Fatalf("expected RELAY_UNAVAILABLE with wrong server_name: err=%v stdout=%q stderr=%q", cerr, out, errOut)
		}
		// (d) Missing server_name against 127.0.0.1 endpoint whose cert has only DNS SAN -> RELAY_UNAVAILABLE.
		missingSNIHandle, err := TurnRelayHandleWithTrust(relayURL, ticket["token"].(string), TurnRelayTrust{
			CASHA256:         caSHA,
			PeerSHA256:       leafSHA,
			MaxResponseBytes: configuredAcqBudget,
		})
		if err != nil {
			t.Fatal(err)
		}
		if out, errOut, cerr := runChildTurnRelay(missingSNIHandle, sandboxCAPath, initLine); cerr == nil || out != "" || !strings.Contains(errOut, "RELAY_UNAVAILABLE") {
			t.Fatalf("expected RELAY_UNAVAILABLE when server_name omitted against DNS-only cert: err=%v stdout=%q stderr=%q", cerr, out, errOut)
		}
		// (e) Wrong peer_sha256 in ticket -> RELAY_TLS_PIN_MISMATCH.
		badPeerHandle, err := TurnRelayHandleWithTrust(relayURL, ticket["token"].(string), TurnRelayTrust{
			CASHA256:         caSHA,
			PeerSHA256:       strings.Repeat("b", 64),
			ServerName:       relayDNSName,
			MaxResponseBytes: configuredAcqBudget,
		})
		if err != nil {
			t.Fatal(err)
		}
		if out, errOut, cerr := runChildTurnRelay(badPeerHandle, sandboxCAPath, initLine); cerr == nil || out != "" || !strings.Contains(errOut, "RELAY_TLS_PIN_MISMATCH") {
			t.Fatalf("expected RELAY_TLS_PIN_MISMATCH with wrong peer_sha256: err=%v stdout=%q stderr=%q", cerr, out, errOut)
		}

		request("close", map[string]any{"capsule_id": capID, "reason": "completed"})
	}

	// 2. Verify cross-process octo-cli --turn-relay directly enforces configured MaxAcquisitionBytes (1024 < 64 MiB)
	// when the relay HTTP response itself exceeds the budget (both via ticket max_response_bytes alone and via header),
	// proving the child process does not fall back to 64 MiB when in-process relayEndpointBudgets is empty.
	oversizeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer oversizeListener.Close()
	oversizeTLS := permission.PublicationTLSListener(oversizeListener, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
	})
	var includeBudgetHeader atomic.Bool
	oversizeSrv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if includeBudgetHeader.Load() {
				w.Header().Set("X-Octo-Acquisition-Budget-Bytes", "1024")
			}
			w.Header().Set("Content-Type", "application/json")
			// Write 2048 bytes (> 1024 budget, far below 64 MiB).
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + strings.Repeat("x", 2048) + `"}` + "\n"))
		}),
	}
	defer oversizeSrv.Close()
	go func() { _ = oversizeSrv.Serve(oversizeTLS) }()

	oversizeURL := "https://" + oversizeListener.Addr().String() + "/mcp"
	// Case 2A: ticket carries max_response_bytes=1024, server omits header -> child still enforces 1024 bytes!
	includeBudgetHeader.Store(false)
	ticketBudgetHandle, err := TurnRelayHandleWithTrust(oversizeURL, strings.Repeat("d", 64), TurnRelayTrust{
		CASHA256:         caSHA,
		PeerSHA256:       leafSHA,
		ServerName:       relayDNSName,
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out, errOut, cerr := runChildTurnRelay(ticketBudgetHandle, sandboxCAPath, initLine); cerr == nil || out != "" || !strings.Contains(errOut, "RESPONSE_BUDGET_EXCEEDED") {
		t.Fatalf("expected child octo-cli --turn-relay to enforce ticket max_response_bytes=1024 (<64MiB): err=%v stdout=%q stderr=%q", cerr, out, errOut)
	}
	// Case 2B: server emits X-Octo-Acquisition-Budget-Bytes=1024 -> child enforces 1024 bytes even if ticket has no max_response_bytes!
	includeBudgetHeader.Store(true)
	headerBudgetHandle, err := TurnRelayHandleWithTrust(oversizeURL, strings.Repeat("d", 64), TurnRelayTrust{
		CASHA256:   caSHA,
		PeerSHA256: leafSHA,
		ServerName: relayDNSName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out, errOut, cerr := runChildTurnRelay(headerBudgetHandle, sandboxCAPath, initLine); cerr == nil || out != "" || !strings.Contains(errOut, "RESPONSE_BUDGET_EXCEEDED") {
		t.Fatalf("expected child octo-cli --turn-relay to enforce X-Octo-Acquisition-Budget-Bytes=1024 (<64MiB): err=%v stdout=%q stderr=%q", cerr, out, errOut)
	}
}
