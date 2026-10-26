package service

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestStep3GroupMemberOperationsRejectEmptyMembersBeforeHTTP(t *testing.T) {
	for _, operation := range step3GroupMemberOperations() {
		t.Run(operation.name, func(t *testing.T) {
			args := step3WithData(operation.args, `{"members":[]}`)
			requests, _, err := runStep3MessagingCommand(t, args)
			assertStep3ValidationBeforeHTTP(t, err, requests)
		})
	}
}

func TestStep3GroupMemberOperationsAcceptNonEmptyMembers(t *testing.T) {
	for _, operation := range step3GroupMemberOperations() {
		t.Run(operation.name, func(t *testing.T) {
			args := step3WithData(operation.args, `{"members":["member-1"]}`)
			requests, body, err := runStep3MessagingCommand(t, args)
			if err != nil || requests != 1 {
				t.Fatalf("non-empty members request: err=%v, HTTP requests=%d; want success and one request", err, requests)
			}
			assertStep3JSONBody(t, body, map[string]any{"members": []any{"member-1"}})
		})
	}
}

func TestStep3GroupUpdateRequiresARecognizedNonNullField(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "empty object", body: `{}`},
		{name: "unknown property only", body: `{"unrelated":"ignored"}`},
		{name: "null name only", body: `{"name":null}`},
		{name: "null notice only", body: `{"notice":null}`},
		{name: "both fields null", body: `{"name":null,"notice":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := step3WithData([]string{"group", "update", "group-1"}, tc.body)
			requests, _, err := runStep3MessagingCommand(t, args)
			assertStep3ValidationBeforeHTTP(t, err, requests)
		})
	}
}

func TestStep3GroupUpdateAcceptsNameOrNoticeAsPresent(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want map[string]any
	}{
		{name: "empty name is a present update", body: `{"name":""}`, want: map[string]any{"name": ""}},
		{name: "empty notice is a present update", body: `{"notice":""}`, want: map[string]any{"notice": ""}},
		{name: "name and notice", body: `{"name":"team","notice":"announcement"}`, want: map[string]any{"name": "team", "notice": "announcement"}},
		{name: "recognized field with unknown property", body: `{"name":"team","unrelated":"ignored"}`, want: map[string]any{"name": "team", "unrelated": "ignored"}},
		{name: "null name with a notice", body: `{"name":null,"notice":"announcement"}`, want: map[string]any{"name": nil, "notice": "announcement"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := step3WithData([]string{"group", "update", "group-1"}, tc.body)
			requests, body, err := runStep3MessagingCommand(t, args)
			if err != nil || requests != 1 {
				t.Fatalf("effective update request: err=%v, HTTP requests=%d; want success and one request", err, requests)
			}
			assertStep3JSONBody(t, body, tc.want)
		})
	}
}

func step3GroupMemberOperations() []struct {
	name string
	args []string
} {
	return []struct {
		name string
		args []string
	}{
		{name: "create", args: []string{"group", "create"}},
		{name: "member-add", args: []string{"group", "member-add", "group-1"}},
		{name: "member-remove", args: []string{"group", "member-remove", "group-1"}},
	}
}

func step3WithData(args []string, body string) []string {
	result := append([]string(nil), args...)
	return append(result, "--data", body)
}

func runStep3MessagingCommand(t *testing.T, args []string) (requests int, body []byte, executeErr error) {
	t.Helper()
	root, tf, srv := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read generated request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "bf_test", Format: "json"}
	cred := &credential.BotCredential{Token: "bf_test", Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{ErrOut: io.Discard}))
	root.SetArgs(args)
	executeErr = root.Execute()
	return requests, body, executeErr
}

func assertStep3ValidationBeforeHTTP(t *testing.T, err error, requests int) {
	t.Helper()
	exitErr := output.AsExitError(err)
	if exitErr == nil || exitErr.Type != "validation" || exitErr.Code != "VALIDATION_ERROR" || requests != 0 {
		t.Fatalf("preflight validation = %#v, HTTP requests=%d; want validation/VALIDATION_ERROR before HTTP", exitErr, requests)
	}
}

func assertStep3JSONBody(t *testing.T, body []byte, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode generated request body %q: %v", body, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("generated request body = %#v, want %#v", got, want)
	}
}
