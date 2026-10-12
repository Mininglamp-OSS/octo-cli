package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// wireCase is one inbound message and the response JSON-RPC 2.0 requires.
// wantID is the exact raw bytes of the "id" member, which must be present on
// every response; wantCode 0 means a result response.
type wireCase struct {
	name     string
	in       string
	noReply  bool
	wantID   string
	wantCode int
}

var wireCases = []wireCase{
	{name: "malformed JSON", in: `{"jsonrpc":"2.0","id":1,`, wantID: "null", wantCode: codeParseError},
	{name: "not JSON", in: `not json`, wantID: "null", wantCode: codeParseError},
	{name: "array (batch) is not a request object", in: `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, wantID: "null", wantCode: codeInvalidRequest},
	{name: "scalar is not a request object", in: `42`, wantID: "null", wantCode: codeInvalidRequest},
	{name: "object id is undeterminable", in: `{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}`, wantID: "null", wantCode: codeInvalidRequest},
	{name: "boolean id is undeterminable", in: `{"jsonrpc":"2.0","id":true,"method":"ping"}`, wantID: "null", wantCode: codeInvalidRequest},
	{name: "wrongly typed method keeps a valid id", in: `{"jsonrpc":"2.0","id":5,"method":7}`, wantID: "5", wantCode: codeInvalidRequest},
	{name: "wrongly typed method with object id", in: `{"jsonrpc":"2.0","id":[1],"method":7}`, wantID: "null", wantCode: codeInvalidRequest},
	{name: "missing method", in: `{"jsonrpc":"2.0","id":"m"}`, wantID: `"m"`, wantCode: codeInvalidRequest},
	{name: "bad version echoes id", in: `{"jsonrpc":"1.0","id":4,"method":"ping"}`, wantID: "4", wantCode: codeInvalidRequest},
	{name: "string id echoed", in: `{"jsonrpc":"2.0","id":"req-1","method":"ping"}`, wantID: `"req-1"`},
	{name: "numeric id echoed", in: `{"jsonrpc":"2.0","id":12,"method":"ping"}`, wantID: "12"},
	{name: "fractional/negative id echoed verbatim", in: `{"jsonrpc":"2.0","id":-1.5,"method":"ping"}`, wantID: "-1.5"},
	{name: "explicit null id is a request", in: `{"jsonrpc":"2.0","id":null,"method":"ping"}`, wantID: "null"},
	{name: "unknown method echoes id", in: `{"jsonrpc":"2.0","id":8,"method":"nope"}`, wantID: "8", wantCode: codeMethodNotFound},
	{name: "missing id is a notification", in: `{"jsonrpc":"2.0","method":"ping"}`, noReply: true},
	{name: "notification for unknown method", in: `{"jsonrpc":"2.0","method":"nope"}`, noReply: true},
}

// assertWire checks one response line byte-level: the "id" member is present
// (not omitted) with exactly the expected value, and the result/error shape
// matches.
func assertWire(t *testing.T, tc wireCase, raw []byte) {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s: response is not a JSON object: %v (%s)", tc.name, err, raw)
	}
	id, ok := obj["id"]
	if !ok {
		t.Fatalf("%s: response omits the required id member: %s", tc.name, raw)
	}
	if string(id) != tc.wantID {
		t.Fatalf("%s: id = %s, want %s", tc.name, id, tc.wantID)
	}
	if string(obj["jsonrpc"]) != `"2.0"` {
		t.Fatalf("%s: jsonrpc = %s", tc.name, obj["jsonrpc"])
	}
	_, hasResult := obj["result"]
	errRaw, hasErr := obj["error"]
	if hasResult == hasErr {
		t.Fatalf("%s: exactly one of result/error must be set: %s", tc.name, raw)
	}
	if tc.wantCode == 0 {
		if !hasResult {
			t.Fatalf("%s: want result, got %s", tc.name, raw)
		}
		return
	}
	var e rpcError
	if err := json.Unmarshal(errRaw, &e); err != nil || e.Code != tc.wantCode {
		t.Fatalf("%s: error code = %d, want %d (%s)", tc.name, e.Code, tc.wantCode, raw)
	}
}

func TestJSONRPCWire_HTTP(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	for _, tc := range wireCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL, "application/json", strings.NewReader(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var body bytes.Buffer
			_, _ = body.ReadFrom(resp.Body)
			if tc.noReply {
				if resp.StatusCode != http.StatusAccepted || body.Len() != 0 {
					t.Fatalf("notification must get 202 with no body, got %d %q", resp.StatusCode, body.String())
				}
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("HTTP %d: %s", resp.StatusCode, body.String())
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q", ct)
			}
			assertWire(t, tc, bytes.TrimSpace(body.Bytes()))
		})
	}
}

// TestJSONRPCWire_Stdio streams every case through one stdio session, then a
// final ping, proving each response is framed on its own line with the right
// id and that malformed/invalid input never desynchronises the stream.
func TestJSONRPCWire_Stdio(t *testing.T) {
	s := newTestServer(t)
	var in strings.Builder
	var expect []wireCase
	for _, tc := range wireCases {
		in.WriteString(tc.in + "\n")
		if !tc.noReply {
			expect = append(expect, tc)
		}
	}
	// An oversized line is a parse error with id null, drained to its newline.
	in.WriteString(`{"jsonrpc":"2.0","id":3,"pad":"` + strings.Repeat("a", maxStdioMessageBytes) + "\"}\n")
	expect = append(expect, wireCase{name: "oversized", wantID: "null", wantCode: codeParseError})
	in.WriteString(`{"jsonrpc":"2.0","id":"after","method":"ping"}` + "\n")
	expect = append(expect, wireCase{name: "resync", wantID: `"after"`})

	var out bytes.Buffer
	if err := s.ServeStdio(context.Background(), strings.NewReader(in.String()), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != len(expect) {
		t.Fatalf("got %d response lines, want %d (notifications must not reply):\n%s", len(lines), len(expect), out.String())
	}
	for i, tc := range expect {
		assertWire(t, tc, []byte(lines[i]))
	}
}

// TestRPCResponse_MarshalsNullID isolates the marshal primitive: a response
// built with no id (either nil or empty) carries an explicit "id":null.
func TestRPCResponse_MarshalsNullID(t *testing.T) {
	for _, id := range []json.RawMessage{nil, {}} {
		for _, resp := range []rpcResponse{
			newErrorResponse(id, codeParseError, "parse error"),
			newResultResponse(id, map[string]any{}),
		} {
			b, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), `"id":null`) {
				t.Fatalf("undetermined id must marshal as null, got %s", b)
			}
		}
	}
	b, _ := json.Marshal(newErrorResponse(json.RawMessage(`"x"`), codeInvalidRequest, "bad"))
	if !strings.Contains(string(b), `"id":"x"`) {
		t.Fatalf("a determined id must be echoed, got %s", b)
	}
}
