package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
		"mail.draft.update":                "/agent-mail-api/webapi/v0/drafts/{id}",
		"mail.me":                          "/agent-mail-api/webapi/v0/identity",
		"mail.message.auto_reply_context":  "/agent-mail-api/webapi/v0/messages/{id}/auto-reply-context",
		"mail.message.list":                "/agent-mail-api/webapi/v0/messages",
		"mail.message.raw":                 "/agent-mail-api/webapi/v0/messages/{id}/raw",
		"mail.message.reply_draft":         "/agent-mail-api/webapi/v0/messages/{id}/reply-draft",
		"mail.message.send":                "/agent-mail-api/webapi/v0/messages",
		"mail.message.send_intent":         "/agent-mail-api/webapi/v0/agent-send-intents",
		"mail.message.read":                "/agent-mail-api/webapi/v0/messages/{id}",
		"mail.message.reply":               "/agent-mail-api/webapi/v0/messages/{id}/reply",
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
		"mail.message.send",
		"mail.message.send_intent",
		"mail.message.reply",
		"mail.message.reply_draft",
		"mail.message.reply_all",
		"mail.message.forward",
		"mail.message.delete",
		"mail.draft.create",
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

func TestMailConfirmationTokensAreSecret(t *testing.T) {
	reg := registry.MustNew()
	for _, operationID := range []string{
		"mail.message.send",
		"mail.message.delete",
		"mail.message.reply",
		"mail.message.reply_all",
		"mail.message.forward",
		"mail.draft.send",
		"mail.draft.delete",
	} {
		op, ok := reg.GetOperation(operationID)
		if !ok {
			t.Errorf("missing operation %s", operationID)
			continue
		}
		var confirmation *registry.ParamInfo
		for i := range op.Parameters {
			if op.Parameters[i].Name == "X-Octo-Confirmation" && op.Parameters[i].In == "header" {
				confirmation = &op.Parameters[i]
				break
			}
		}
		if confirmation == nil {
			t.Errorf("%s has no X-Octo-Confirmation header", operationID)
			continue
		}
		if !confirmation.Secret {
			t.Errorf("%s confirmation token is not marked secret", operationID)
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

func TestMailSendForwardsServerConfirmationToken(t *testing.T) {
	var gotConfirmation string
	mailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotConfirmation = r.Header.Get("X-Octo-Confirmation")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"messageId":"E1","submissionIds":["S1"]}`))
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
		"mail", "message", "send",
		"--to", "recipient@example.com",
		"--subject", "Confirmed",
		"--text", "Body",
		"--confirmation-token", "omc_once",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute confirmed send: %v\n%s", err, tf.ErrOut.String())
	}
	if gotConfirmation != "omc_once" {
		t.Fatalf("X-Octo-Confirmation = %q", gotConfirmation)
	}
}
