package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

func TestMailJMAPStateAndChanges(t *testing.T) {
	var sawMailToken bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer omb_mail_secret" {
			sawMailToken = true
		}
		if r.Header.Get("X-Space-Id") != "" {
			t.Errorf("JMAP request carried X-Space-Id = %q", r.Header.Get("X-Space-Id"))
		}
		switch r.URL.Path {
		case mailJMAPSessionPath:
			writeTestJSON(w, http.StatusOK, map[string]any{
				"primaryAccounts": map[string]string{mailJMAPCapability: "42"},
			})
		case mailJMAPAPIPath:
			var request struct {
				MethodCalls [][3]json.RawMessage `json:"methodCalls"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			var method string
			var arguments map[string]any
			_ = json.Unmarshal(request.MethodCalls[0][0], &method)
			_ = json.Unmarshal(request.MethodCalls[0][1], &arguments)
			switch method {
			case "Email/get":
				writeTestJSON(w, http.StatusOK, map[string]any{
					"methodResponses": []any{[]any{"Email/get", map[string]any{
						"accountId": "42", "state": "7", "list": []any{}, "notFound": []any{},
					}, "mail-cli"}},
				})
			case "Email/changes":
				if arguments["accountId"] != "42" || arguments["sinceState"] != "7" || arguments["maxChanges"] != float64(10) {
					t.Errorf("Email/changes arguments = %#v", arguments)
				}
				writeTestJSON(w, http.StatusOK, map[string]any{
					"methodResponses": []any{[]any{"Email/changes", map[string]any{
						"accountId": "42", "oldState": "7", "newState": "9", "hasMoreChanges": false,
						"created": []string{"E8"}, "updated": []string{}, "destroyed": []string{},
					}, "mail-cli"}},
				})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	factory := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: server.URL, BotToken: "app_bot", Format: "json"}
	bot := &credential.BotCredential{Token: "app_bot", Profile: "agent", RobotID: "robot-1", SpaceID: "space-1"}
	mailCredential := &credential.MailCredential{Token: "omb_mail_secret", BotProfile: "agent"}
	factory.SetConfig(cfg)
	factory.SetCredential(bot)
	factory.SetMailCredential(mailCredential)
	factory.SetMailClient(client.NewMail(cfg, mailCredential, client.Options{ErrOut: io.Discard, NoRetry: true}))

	out, _, err := execRoot(t, factory, "mail", "message", "state")
	if err != nil {
		t.Fatalf("mail message state: %v", err)
	}
	state := dataOf(t, out)
	if state["account_id"] != "42" || state["state"] != "7" {
		t.Fatalf("state output = %#v", state)
	}

	factory.Out.Reset()
	factory.ErrOut.Reset()
	out, _, err = execRoot(t, factory, "mail", "message", "changes", "--since-state", "7", "--max-changes", "10")
	if err != nil {
		t.Fatalf("mail message changes: %v", err)
	}
	changes := dataOf(t, out)
	if changes["newState"] != "9" || len(changes["created"].([]any)) != 1 {
		t.Fatalf("changes output = %#v", changes)
	}
	if !sawMailToken {
		t.Fatal("JMAP commands did not use the bound Agent Mail credential")
	}
}

func TestMailJMAPStateAndChangesDryRunStopsAfterSessionPreview(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "state", args: []string{"mail", "message", "state", "--dry-run"}},
		{name: "changes", args: []string{"mail", "message", "changes", "--since-state", "7", "--dry-run"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := newTestFactoryWithReg()
			cfg := &config.Config{APIBaseURL: "http://mail.invalid", BotToken: "app_bot", Format: "json"}
			bot := &credential.BotCredential{Token: "app_bot", Profile: "agent", RobotID: "robot-1"}
			mailCredential := &credential.MailCredential{Token: "omb_mail_secret", BotID: "robot-1", BotProfile: "agent"}
			factory.SetConfig(cfg)
			factory.SetCredential(bot)
			factory.SetMailCredential(mailCredential)
			factory.SetMailClient(client.NewMail(cfg, mailCredential, client.Options{
				ErrOut: io.Discard, DryRun: true, NoRetry: true,
			}))

			out, stderr, err := execRoot(t, factory, tt.args...)
			if err != nil {
				t.Fatalf("%s --dry-run: %v\nstderr=%s", tt.name, err, stderr)
			}
			preview := dataOf(t, out)
			if preview["dry_run"] != true || preview["method"] != http.MethodGet {
				t.Fatalf("dry-run preview = %#v", preview)
			}
			url, _ := preview["url"].(string)
			if !strings.HasSuffix(url, mailJMAPSessionPath) {
				t.Fatalf("dry-run URL = %q, want JMAP session preview", url)
			}
			if strings.Contains(stderr, "JMAP_MAIL_UNAVAILABLE") || strings.Contains(stderr, "reconnect the Agent mailbox") {
				t.Fatalf("dry-run emitted a spurious authorization error: %s", stderr)
			}
		})
	}
}
