package mcp

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestRound15FlatArgumentKeyBudgetBothFacades(t *testing.T) {
	for _, def := range append(toolDefinitions(), twoToolDefinitions()...) {
		if def.Name != "call_op" && def.Name != "execute" {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				MaxProperties int `json:"maxProperties"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(def.InputSchema, &schema); err != nil || schema.Properties["arguments"].MaxProperties != maxMCPObjectKeys {
			t.Fatalf("%s schema does not advertise %d business keys: %s (%v)", def.Name, maxMCPObjectKeys, def.InputSchema, err)
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
	for _, facade := range []facadeMode{facadeTwo, facadeThree} {
		h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facade)
		if err != nil {
			t.Fatal(err)
		}
		tool := "execute"
		if facade == facadeThree {
			tool = "call_op"
		}
		raw := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":{"operation_id":"docs.list","arguments":%s}}}`, tool, oversized)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("POST", "/", strings.NewReader(raw)))
		var got rpcResponse
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatalf("invalid response: %s (%v)", response.Body.String(), err)
		}
		if facade == facadeThree {
			if got.Error == nil || got.Error.Code != codeInvalidParams {
				t.Fatalf("call_op accepted 513 keys: %s", response.Body.String())
			}
		} else {
			var tool struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			}
			if err := json.Unmarshal(got.Result, &tool); err != nil || !tool.IsError || len(tool.Content) != 1 {
				t.Fatalf("invalid execute result: %s (%v)", response.Body.String(), err)
			}
			var payload struct {
				Status string `json:"status"`
				Error  struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(tool.Content[0].Text), &payload); err != nil || payload.Status != "validation_error" || !strings.HasPrefix(payload.Error.Message, "too many object keys") {
				t.Fatalf("execute accepted 513 keys: %s (%v)", response.Body.String(), err)
			}
		}
	}
}
