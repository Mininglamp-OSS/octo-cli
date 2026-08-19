package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestMailCommandUsesMailEndpointAndCredential(t *testing.T) {
	var gotPath, gotAuth, gotQuery, gotSpace string
	mailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		gotSpace = r.Header.Get("X-Space-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[],"total":0,"offset":0,"limit":10}`))
	}))
	defer mailServer.Close()

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{
		APIBaseURL: mailServer.URL,
		BotToken:   "app_test",
		Format:     "json",
	}
	tf.SetConfig(cfg)
	tf.SetCredential(&credential.BotCredential{Token: "app_test", SpaceID: "space-1", Source: "test"})
	mailCredential := &credential.MailCredential{Token: "omb_mail_secret", Source: "test"}
	tf.SetMailCredential(mailCredential)
	tf.SetMailClient(client.NewMail(cfg, mailCredential, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	root.SetArgs([]string{"mail", "message", "list", "--mailbox", "Inbox", "--unread=true", "--limit", "10"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute mail list: %v\n%s", err, tf.ErrOut.String())
	}

	if gotPath != "/agent-mail-api/webapi/v0/messages" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer omb_mail_secret" {
		t.Errorf("Authorization = %q, want Agent Mail credential", gotAuth)
	}
	if !strings.Contains(gotQuery, "mailbox=Inbox") || !strings.Contains(gotQuery, "unread=true") || !strings.Contains(gotQuery, "limit=10") {
		t.Errorf("query = %q", gotQuery)
	}
	if gotSpace != "" {
		t.Errorf("mail request must not carry Bot space context; got %q", gotSpace)
	}
}

func TestMailRegistrySurface(t *testing.T) {
	reg := registry.MustNew()
	want := map[string]string{
		"mail.address.list":                "/agent-mail-api/webapi/v0/addresses",
		"mail.draft.create_agent":          "/agent-mail-api/webapi/v0/agent-drafts",
		"mail.draft.delete":                "/agent-mail-api/webapi/v0/drafts/{id}",
		"mail.draft.list":                  "/agent-mail-api/webapi/v0/drafts",
		"mail.draft.send":                  "/agent-mail-api/webapi/v0/drafts/{id}/send",
		"mail.draft.update":                "/agent-mail-api/webapi/v0/drafts/{id}",
		"mail.me":                          "/agent-mail-api/webapi/v0/identity",
		"mail.message.auto_reply_context":  "/agent-mail-api/webapi/v0/messages/{id}/auto-reply-context",
		"mail.message.list":                "/agent-mail-api/webapi/v0/messages",
		"mail.message.raw":                 "/agent-mail-api/webapi/v0/messages/{id}/raw",
		"mail.message.reply_draft":         "/agent-mail-api/webapi/v0/messages/{id}/reply-draft",
		"mail.message.send_intent":         "/agent-mail-api/webapi/v0/agent-send-intents",
		"mail.message.read":                "/agent-mail-api/webapi/v0/messages/{id}",
		"mail.message.delivery":            "/agent-mail-api/webapi/v0/messages/{id}/delivery",
		"mail.message.attachment.download": "/agent-mail-api/webapi/v0/messages/{id}/attachments/{partId}",
		"mail.thread.get":                  "/agent-mail-api/webapi/v0/threads/{id}",
	}
	for id, path := range want {
		op, ok := reg.GetOperation(id)
		if !ok {
			t.Errorf("missing operation %s", id)
			continue
		}
		if op.Path != path {
			t.Errorf("%s path = %q, want %q", id, op.Path, path)
		}
		if op.BaseURLEnv != config.EnvAPIBaseURL {
			t.Errorf("%s base URL env = %q", id, op.BaseURLEnv)
		}
		if op.Credential != "mail" {
			t.Errorf("%s credential = %q, want mail", id, op.Credential)
		}
	}
	list, ok := reg.GetOperation("mail.message.list")
	if !ok {
		t.Fatal("missing operation mail.message.list")
	}
	var unreadParamFound bool
	for _, parameter := range list.Parameters {
		if parameter.Name == "unread" && parameter.In == "query" && parameter.Type == "boolean" {
			unreadParamFound = true
			break
		}
	}
	if !unreadParamFound {
		t.Fatalf("mail.message.list parameters = %v, want boolean unread query", list.Parameters)
	}

	for _, id := range []string{
		"mail.message.send_intent",
		"mail.message.reply_draft",
		"mail.draft.create_agent",
		"mail.draft.update",
		"mail.draft.send",
		"mail.draft.delete",
	} {
		op, ok := reg.GetOperation(id)
		if !ok {
			t.Errorf("missing operation %s", id)
			continue
		}
		if op.RetryMode != "never" {
			t.Errorf("%s retry mode = %q, want never", id, op.RetryMode)
		}
	}

	sendIntent, ok := reg.GetOperation("mail.message.send_intent")
	if !ok || sendIntent.ResponseSchema == nil {
		t.Fatal("mail.message.send_intent must expose its structured response schema")
	}
	if _, ok := sendIntent.ResponseSchema.Properties["senderAddress"]; !ok {
		t.Fatal("mail.message.send_intent response must expose authoritative senderAddress")
	}
}

func TestMailRegistryExposesAgentDraftOperationsWithoutConfirmationFlow(t *testing.T) {
	reg := registry.MustNew()
	for _, operationID := range []string{
		"mail.message.send",
		"mail.message.delete",
		"mail.message.reply",
		"mail.message.reply_all",
		"mail.message.forward",
		"mail.draft.create",
	} {
		if _, ok := reg.GetOperation(operationID); ok {
			t.Errorf("owner-only operation %s must not be exposed by Agent Mail", operationID)
		}
	}
	for operationID, method := range map[string]string{
		"mail.draft.update": http.MethodPatch,
		"mail.draft.send":   http.MethodPost,
		"mail.draft.delete": http.MethodDelete,
	} {
		op, ok := reg.GetOperation(operationID)
		if !ok {
			t.Errorf("explicit Agent Draft operation %s must be exposed", operationID)
			continue
		}
		if op.Method != method {
			t.Errorf("%s method = %q, want %q", operationID, op.Method, method)
		}
		if op.RetryMode != "never" {
			t.Errorf("%s retry mode = %q, want never", operationID, op.RetryMode)
		}
	}
	for _, operationID := range []string{"mail.draft.update", "mail.draft.send"} {
		op, ok := reg.GetOperation(operationID)
		if !ok {
			continue
		}
		if op.RequestBody == nil || !slices.Contains(op.RequestBody.Required, "draftVersion") {
			t.Errorf("%s must require draftVersion", operationID)
		}
	}
	for _, info := range reg.ListOperations("mail") {
		op, ok := reg.GetOperation(info.ID)
		if !ok {
			t.Fatalf("mail operation %s disappeared", info.ID)
		}
		for _, parameter := range op.Parameters {
			if parameter.Name == "X-Octo-Confirmation" || parameter.FlagName == "confirmation-token" {
				t.Errorf("%s still exposes the deleted Agent self-confirmation flow", info.ID)
			}
		}
	}
}

func TestMailDraftCommandsUseVersionedAgentDraftEndpoints(t *testing.T) {
	type observedRequest struct {
		method, path, confirmation, automation string
		body                                   map[string]any
	}
	var requests []observedRequest
	mailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && r.Method != http.MethodDelete {
				http.Error(w, "invalid test request body", http.StatusBadRequest)
				return
			}
		}
		requests = append(requests, observedRequest{
			method: r.Method, path: r.URL.Path,
			confirmation: r.Header.Get("X-Octo-Confirmation"),
			automation:   r.Header.Get("X-Octo-Automation"),
			body:         body,
		})
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPatch:
			_, _ = w.Write([]byte(`{"id":"E2","draftVersion":2}`))
		case http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"outcome":"accepted","messageId":"E9","submissionIds":[1],"senderAddress":"agent@example.com"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer mailServer.Close()

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: mailServer.URL, BotToken: "app_test", Format: "json"}
	tf.SetConfig(cfg)
	tf.SetCredential(&credential.BotCredential{Token: "app_test", Source: "test"})
	mailCredential := &credential.MailCredential{Token: "omb_mail_secret", Source: "test"}
	tf.SetMailCredential(mailCredential)
	tf.SetMailClient(client.NewMail(cfg, mailCredential, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	execute := func(args ...string) error {
		root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
		RegisterServiceCommands(root, tf.Factory)
		root.SetArgs(args)
		return root.Execute()
	}
	run := func(args ...string) {
		t.Helper()
		if err := execute(args...); err != nil {
			t.Fatalf("execute %v: %v\n%s", args, err, tf.ErrOut.String())
		}
	}
	for _, args := range [][]string{
		{"mail", "draft", "update", "E1", "--to", "recipient@example.com", "--subject", "Updated"},
		{"mail", "draft", "send", "E1"},
	} {
		before := len(requests)
		err := execute(args...)
		if err == nil || !strings.Contains(err.Error(), "draftVersion") {
			t.Fatalf("execute %v error = %v, want missing draftVersion", args, err)
		}
		if len(requests) != before {
			t.Fatalf("execute %v sent %d request(s), want local validation failure", args, len(requests)-before)
		}
	}
	run("mail", "draft", "update", "E1", "--draft-version", "1", "--to", "recipient@example.com", "--subject", "Updated", "--text", "Body")
	run("mail", "draft", "send", "E2", "--draft-version", "2")
	run("mail", "draft", "delete", "E3")

	want := []struct {
		method, path string
		version      float64
	}{
		{http.MethodPatch, "/agent-mail-api/webapi/v0/drafts/E1", 1},
		{http.MethodPost, "/agent-mail-api/webapi/v0/drafts/E2/send", 2},
		{http.MethodDelete, "/agent-mail-api/webapi/v0/drafts/E3", 0},
	}
	if len(requests) != len(want) {
		t.Fatalf("requests = %#v, want %d", requests, len(want))
	}
	for i, expected := range want {
		got := requests[i]
		if got.method != expected.method || got.path != expected.path {
			t.Errorf("request %d = %s %s, want %s %s", i, got.method, got.path, expected.method, expected.path)
		}
		if got.confirmation != "" || got.automation != "" {
			t.Errorf("request %d unexpectedly carries confirmation=%q automation=%q", i, got.confirmation, got.automation)
		}
		if expected.version > 0 && got.body["draftVersion"] != expected.version {
			t.Errorf("request %d draftVersion = %#v, want %v", i, got.body["draftVersion"], expected.version)
		}
	}
}

func TestMailSendIntentUsesPolicyEndpointAndDoesNotRetry(t *testing.T) {
	var calls int32
	var gotIdempotency string
	mailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/agent-mail-api/webapi/v0/agent-send-intents" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotIdempotency = r.Header.Get("X-Octo-Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"try later"}}`))
	}))
	defer mailServer.Close()

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: mailServer.URL, BotToken: "app_test", Format: "json"}
	tf.SetConfig(cfg)
	tf.SetCredential(&credential.BotCredential{Token: "app_test", Source: "test"})
	mailCredential := &credential.MailCredential{Token: "omb_mail_secret", Source: "test"}
	tf.SetMailCredential(mailCredential)
	tf.SetMailClient(client.NewMail(cfg, mailCredential, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	root.SetArgs([]string{
		"mail", "message", "send-intent",
		"--to", "recipient@example.com",
		"--subject", "Policy-aware send",
		"--text", "Body",
		"--idempotency-key", "intent-0001",
	})
	if err := root.Execute(); err == nil {
		t.Fatal("execute send intent: expected 503 error")
	}
	if gotIdempotency != "intent-0001" {
		t.Fatalf("X-Octo-Idempotency-Key = %q", gotIdempotency)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("send intent requests = %d, want exactly 1", got)
	}
}

func TestMailSendIntentAttachmentUsesBackendContentField(t *testing.T) {
	var gotContent string
	mailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Attachments []struct {
				Content string `json:"content"`
			} `json:"attachments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if len(body.Attachments) != 1 {
			t.Fatalf("attachments = %#v", body.Attachments)
		}
		gotContent = body.Attachments[0].Content
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"outcome":"accepted","status":"accepted","messageId":"E1"}`))
	}))
	defer mailServer.Close()

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: mailServer.URL, BotToken: "app_test", Format: "json"}
	tf.SetConfig(cfg)
	tf.SetCredential(&credential.BotCredential{Token: "app_test", Source: "test"})
	mailCredential := &credential.MailCredential{Token: "omb_mail_secret", Source: "test"}
	tf.SetMailCredential(mailCredential)
	tf.SetMailClient(client.NewMail(cfg, mailCredential, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	root.SetArgs([]string{
		"mail", "message", "send-intent",
		"--data", `{"to":["recipient@example.com"],"subject":"Attachment","attachments":[{"filename":"image.png","contentType":"image/png","content":"aW1hZ2U="}]}`,
		"--idempotency-key", "attachment-intent-0001",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute attachment send intent: %v\n%s", err, tf.ErrOut.String())
	}
	if gotContent != "aW1hZ2U=" {
		t.Fatalf("attachment content = %q", gotContent)
	}
}

func TestMailAttachmentDownloadWritesOutput(t *testing.T) {
	mailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent-mail-api/webapi/v0/messages/E1/attachments/1.2" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer omb_mail_secret" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", `attachment; filename="report.txt"`)
		_, _ = w.Write([]byte("attachment body"))
	}))
	defer mailServer.Close()

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: mailServer.URL, BotToken: "app_test", Format: "json"}
	tf.SetConfig(cfg)
	tf.SetCredential(&credential.BotCredential{Token: "app_test", Source: "test"})
	mailCredential := &credential.MailCredential{Token: "omb_mail_secret", Source: "test"}
	tf.SetMailCredential(mailCredential)
	tf.SetMailClient(client.NewMail(cfg, mailCredential, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	outputPath := t.TempDir() + "/report.txt"
	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	root.SetArgs([]string{"mail", "message", "attachment", "download", "E1", "1.2", "--output", outputPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("download attachment: %v\n%s", err, tf.ErrOut.String())
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "attachment body" {
		t.Fatalf("downloaded content = %q", content)
	}
}
