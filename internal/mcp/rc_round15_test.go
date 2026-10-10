package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestRound15DuplicateRPCMethodRejectedHTTPAndStdio(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":1,"method":"ping","method":"tools/list"}`
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", "")
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("POST", "/", strings.NewReader(raw)))
	var got rpcResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Error == nil || got.Error.Code != codeInvalidRequest {
		t.Fatalf("HTTP accepted duplicate method: %s (%v)", response.Body.String(), err)
	}

	var stdio bytes.Buffer
	if err := newTestServer(t).ServeStdio(context.Background(), strings.NewReader(raw+"\n"), &stdio); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stdio.Bytes(), &got); err != nil || got.Error == nil || got.Error.Code != codeInvalidRequest {
		t.Fatalf("stdio accepted duplicate method: %s (%v)", stdio.String(), err)
	}
}

func TestRound15AmbiguousRoutingObjectsFailClosed(t *testing.T) {
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", "")
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t)
	cases := []struct {
		name string
		raw  string
		code int
	}{
		{"duplicate-id", `{"jsonrpc":"2.0","id":1,"id":2,"method":"ping"}`, codeInvalidRequest},
		{"duplicate-params", `{"jsonrpc":"2.0","id":1,"method":"ping","params":{},"params":null}`, codeInvalidRequest},
		{"escaped-duplicate-method", `{"jsonrpc":"2.0","id":1,"method":"ping","m\u0065thod":"tools/list"}`, codeInvalidRequest},
		{"unknown-envelope-key", `{"jsonrpc":"2.0","id":1,"method":"ping","extra":true}`, codeInvalidRequest},
		{"duplicate-tool-name", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_ops","name":"call_op","arguments":{}}}`, codeInvalidParams},
		{"duplicate-search-query", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_ops","arguments":{"query":"safe","query":"unsafe"}}}`, codeInvalidParams},
		{"duplicate-operation-id", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"call_op","arguments":{"operation_id":"docs.list","operation_id":"message.send","arguments":{}}}}`, codeInvalidParams},
		{"duplicate-operation-argument", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"call_op","arguments":{"operation_id":"message.send","arguments":{"content":"one","content":"two"}}}}`, codeInvalidParams},
		{"duplicate-resource-uri", `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"skill://docs","uri":"skill://message"}}`, codeInvalidParams},
		{"duplicate-initialize-version", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","protocolVersion":"1.0"}}`, codeInvalidParams},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest("POST", "/", strings.NewReader(tc.raw)))
			var got rpcResponse
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Error == nil || got.Error.Code != tc.code {
				t.Fatalf("HTTP code mismatch: %s (%v), want %d", response.Body.String(), err, tc.code)
			}
			var stdio bytes.Buffer
			if err := s.ServeStdio(context.Background(), strings.NewReader(tc.raw+"\n"), &stdio); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(stdio.Bytes(), &got); err != nil || got.Error == nil || got.Error.Code != tc.code {
				t.Fatalf("stdio code mismatch: %s (%v), want %d", stdio.String(), err, tc.code)
			}
		})
	}
}

func TestRound15NullParamsAndIDRemainCompatible(t *testing.T) {
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", "")
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("POST", "/", strings.NewReader(`{"jsonrpc":"2.0","id":null,"method":"ping","params":null}`)))
	var got rpcResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Error != nil || string(got.ID) != "null" || string(got.Result) != "{}" {
		t.Fatalf("valid JSON-RPC null values changed: %s (%v)", response.Body.String(), err)
	}
}

func TestRound15FlatArgumentKeyBudget(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			MaxProperties int `json:"maxProperties"`
		} `json:"properties"`
	}
	for _, def := range toolDefinitions() {
		if def.Name != "call_op" {
			continue
		}
		if err := json.Unmarshal(def.InputSchema, &schema); err != nil || schema.Properties["arguments"].MaxProperties != maxMCPObjectKeys {
			t.Fatalf("call_op schema does not advertise %d business keys: %s (%v)", maxMCPObjectKeys, def.InputSchema, err)
		}
	}
	build := func(n int) json.RawMessage {
		values := make(map[string]int, n)
		for i := range n {
			values[fmt.Sprintf("field%d", i)] = i
		}
		raw, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if err := validateObjectKeysPolicy(build(maxMCPObjectKeys), true); err != nil {
		t.Fatalf("allowed 512-key business object rejected: %v", err)
	}
	oversized := build(maxMCPObjectKeys + 1)
	if err := validateObjectKeysPolicy(oversized, true); err == nil {
		t.Fatal("513-key business object was accepted")
	}

	t.Setenv("OCTO_MCP_UPLOAD_ROOT", "")
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"call_op","arguments":{"operation_id":"docs.list","arguments":%s}}}`, oversized)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("POST", "/", strings.NewReader(raw)))
	var got rpcResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Error == nil || got.Error.Code != codeInvalidParams {
		t.Fatalf("HTTP 513-key business object did not fail before execution: %s (%v)", response.Body.String(), err)
	}
	var stdio bytes.Buffer
	if err := newTestServer(t).ServeStdio(context.Background(), strings.NewReader(raw+"\n"), &stdio); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stdio.Bytes(), &got); err != nil || got.Error == nil || got.Error.Code != codeInvalidParams {
		t.Fatalf("stdio 513-key business object did not fail before execution: %s (%v)", stdio.String(), err)
	}
}
