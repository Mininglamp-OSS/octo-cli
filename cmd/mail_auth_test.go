package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

func mailBindingKey(t *testing.T, robotID, spaceID, botToken, apiOrigin string) string {
	t.Helper()
	key, err := authstore.MailBindingKey(robotID, spaceID, botToken, apiOrigin)
	if err != nil {
		t.Fatalf("MailBindingKey: %v", err)
	}
	return key
}

func TestMailAuthLoginStatusAndCredentialStorage(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	var approved atomic.Bool
	var revoked atomic.Bool
	var sawPublicDeviceRequest, sawMailToken atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/register":
			if r.Header.Get("Authorization") != "Bearer app_bot_token" {
				t.Errorf("Bot identity authorization header = %q", r.Header.Get("Authorization"))
			}
			writeTestJSON(w, http.StatusOK, map[string]any{"robot_id": "robot-1", "name": "Agent"})
		case mailDevicePath:
			if r.Header.Get("Authorization") != "" {
				t.Errorf("device authorization header = %q, want empty", r.Header.Get("Authorization"))
			}
			sawPublicDeviceRequest.Store(true)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["botId"] != "robot-1" || body["botProfile"] != "agent" ||
				body["mailboxAddress"] != "agent@example.com" || body["spaceId"] != "space-1" ||
				body["codeChallenge"] == "" {
				t.Errorf("device request = %#v", body)
			}
			writeTestJSON(w, http.StatusOK, map[string]any{
				"deviceCode": "omd_device_secret", "userCode": "ABCD-EFGH",
				"verificationUri":         "https://octo.example/mail/authorize",
				"verificationUriComplete": "https://octo.example/mail/authorize?code=ABCD-EFGH",
				"expiresIn":               600, "interval": 3,
			})
		case mailTokenPath:
			if r.Header.Get("Authorization") != "" {
				t.Errorf("token exchange authorization header = %q, want empty", r.Header.Get("Authorization"))
			}
			if !approved.Load() {
				writeTestJSON(w, http.StatusBadRequest, map[string]any{
					"error": map[string]any{"code": "authorization_pending", "message": "waiting"},
				})
				return
			}
			writeTestJSON(w, http.StatusOK, map[string]any{
				"accessToken": "omb_bound_secret", "mailboxAddress": "agent@example.com",
				"botId": "robot-1", "botProfile": "agent",
			})
		case "/agent-mail-api/webapi/v0/identity":
			if r.Header.Get("Authorization") == "Bearer omb_bound_secret" {
				sawMailToken.Store(true)
			}
			if revoked.Load() {
				writeTestJSON(w, http.StatusUnauthorized, map[string]any{
					"error": map[string]any{"code": "unauthorized", "message": "authentication failed"},
				})
				return
			}
			writeTestJSON(w, http.StatusOK, map[string]any{"address": "agent@example.com"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	newFactory := func() *cmdutil.TestFactory {
		f := newTestFactoryWithReg()
		cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_bot_token", Format: "json"}
		bot := &credential.BotCredential{
			Token: "app_bot_token", Profile: "agent", RobotID: "robot-1", SpaceID: "space-1", Source: "profile:agent",
		}
		f.SetConfig(cfg)
		f.SetCredential(bot)
		f.SetClient(client.New(cfg, bot, client.Options{ErrOut: io.Discard, NoRetry: true}))
		return f
	}

	f := newFactory()
	out, _, err := execRoot(t, f, "mail", "auth", "login", "--profile", "agent", "--mailbox", "agent@example.com")
	if err != nil {
		t.Fatalf("mail auth login: %v\n%s", err, f.ErrOut.String())
	}
	if !sawPublicDeviceRequest.Load() {
		t.Error("device authorization request was not sent")
	}
	if strings.Contains(out, "omd_device_secret") {
		t.Error("login output leaked the device code")
	}
	loginData := dataOf(t, out)
	if loginData["status"] != "authorization_required" || loginData["user_code"] != "ABCD-EFGH" {
		t.Fatalf("login data = %#v", loginData)
	}
	if loginData["requested_mailbox_address"] != "agent@example.com" {
		t.Fatalf("target mailbox missing from login data = %#v", loginData)
	}

	f = newFactory()
	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	bindingKey := mailBindingKey(t, "robot-1", "space-1", "app_bot_token", srv.URL)
	pending, err := store.PendingMailAuthorization(bindingKey)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err = execRoot(t, f, "mail", "auth", "status", "--profile", "agent", "--verbose")
	if err != nil {
		t.Fatalf("pending status: %v\n%s", err, f.ErrOut.String())
	}
	if dataOf(t, out)["status"] != "pending" {
		t.Fatalf("pending data = %#v", dataOf(t, out))
	}
	trace := f.ErrOut.String()
	for _, secret := range []string{pending.DeviceCode, pending.CodeVerifier} {
		if strings.Contains(trace, secret) {
			t.Fatalf("pending status verbose trace leaked authorization proof %q: %s", secret, trace)
		}
	}

	approved.Store(true)
	f = newFactory()
	out, _, err = execRoot(t, f, "mail", "auth", "status", "--profile", "agent")
	if err != nil {
		t.Fatalf("complete status: %v\n%s", err, f.ErrOut.String())
	}
	connected := dataOf(t, out)
	if connected["status"] != "connected" || connected["mailbox_address"] != "agent@example.com" {
		t.Fatalf("connected data = %#v", connected)
	}
	token, err := store.GetMailCredential(bindingKey)
	if err != nil || token != "omb_bound_secret" {
		t.Fatalf("stored mail credential = %q, %v", token, err)
	}

	// With no pending flow, status verifies the stored credential against the
	// mailbox identity endpoint instead of trusting local state.
	f = newFactory()
	out, _, err = execRoot(t, f, "mail", "auth", "status", "--profile", "agent")
	if err != nil {
		t.Fatalf("connected status: %v\n%s", err, f.ErrOut.String())
	}
	if dataOf(t, out)["status"] != "connected" || !sawMailToken.Load() {
		t.Fatalf("verified connection = %#v, mail token seen=%v", dataOf(t, out), sawMailToken.Load())
	}

	// Rebinding or unbinding revokes the server credential. Status converts the
	// resulting API 401 into an unconnected state and removes stale local state.
	revoked.Store(true)
	f = newFactory()
	out, _, err = execRoot(t, f, "mail", "auth", "status", "--profile", "agent")
	if err != nil {
		t.Fatalf("revoked status: %v\n%s", err, f.ErrOut.String())
	}
	if dataOf(t, out)["status"] != "unconnected" {
		t.Fatalf("revoked connection = %#v", dataOf(t, out))
	}
	if _, err := store.GetMailCredential(bindingKey); !errors.Is(err, authstore.ErrMailCredentialNotFound) {
		t.Fatalf("revoked mail credential was not removed: %v", err)
	}
}

func TestMailAuthResolvesRuntimeBotIdentityFromToken(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	var sawDeviceRequest atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/register":
			if r.Header.Get("Authorization") != "Bearer app_runtime_token" {
				t.Errorf("Bot identity authorization header = %q", r.Header.Get("Authorization"))
			}
			writeTestJSON(w, http.StatusOK, map[string]any{"robot_id": "runtime-bot"})
		case mailDevicePath:
			if r.Header.Get("Authorization") != "" {
				t.Errorf("device authorization header = %q, want empty", r.Header.Get("Authorization"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["botId"] != "runtime-bot" || body["spaceId"] != "space-runtime" || body["codeChallenge"] == "" {
				t.Errorf("device request = %#v", body)
			}
			if _, exists := body["botProfile"]; exists {
				t.Errorf("runtime device request must not invent a profile: %#v", body)
			}
			sawDeviceRequest.Store(true)
			writeTestJSON(w, http.StatusOK, map[string]any{
				"deviceCode": "omd_runtime_secret", "userCode": "RUNTIME-1",
				"verificationUri":         "https://octo.example/mail/authorize",
				"verificationUriComplete": "https://octo.example/mail/authorize?code=RUNTIME-1",
				"expiresIn":               600, "interval": 3,
			})
		case mailTokenPath:
			writeTestJSON(w, http.StatusOK, map[string]any{
				"accessToken": "omb_runtime_secret", "mailboxAddress": "runtime@example.com",
				"botId": "runtime-bot",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	newFactory := func() *cmdutil.TestFactory {
		f := newTestFactoryWithReg()
		cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_runtime_token", Format: "json"}
		bot := &credential.BotCredential{
			Token: "app_runtime_token", BotKind: "app_bot", SpaceID: "space-runtime",
			Source: "env:OCTO_BOT_TOKEN",
		}
		f.SetConfig(cfg)
		f.SetCredential(bot)
		f.SetClient(client.New(cfg, bot, client.Options{ErrOut: io.Discard, NoRetry: true}))
		return f
	}

	f := newFactory()
	out, _, err := execRoot(t, f, "mail", "auth", "login")
	if err != nil {
		t.Fatalf("mail auth login: %v\n%s", err, f.ErrOut.String())
	}
	login := dataOf(t, out)
	if login["bot_id"] != "runtime-bot" || login["profile"] != nil || !sawDeviceRequest.Load() {
		t.Fatalf("login data = %#v", login)
	}

	f = newFactory()
	out, _, err = execRoot(t, f, "mail", "auth", "status")
	if err != nil {
		t.Fatalf("mail auth status: %v\n%s", err, f.ErrOut.String())
	}
	connected := dataOf(t, out)
	if connected["status"] != "connected" || connected["bot_id"] != "runtime-bot" || connected["profile"] != nil {
		t.Fatalf("connected data = %#v", connected)
	}

	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	bindingKey := mailBindingKey(t, "runtime-bot", "space-runtime", "app_runtime_token", srv.URL)
	mailToken, err := store.GetMailCredential(bindingKey)
	if err != nil || mailToken != "omb_runtime_secret" {
		t.Fatalf("stored mail credential = %q, %v", mailToken, err)
	}
	if _, err := store.GetMailCredential(""); !errors.Is(err, authstore.ErrMailCredentialNotFound) {
		t.Fatalf("empty profile unexpectedly stored a credential: %v", err)
	}
}

func TestMailAuthRejectsClaimedBotIDThatDoesNotOwnToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/bot/register" {
			http.NotFound(w, r)
			return
		}
		writeTestJSON(w, http.StatusOK, map[string]any{"robot_id": "actual-bot"})
	}))
	defer srv.Close()

	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_runtime_token", Format: "json"}
	bot := &credential.BotCredential{
		Token: "app_runtime_token", RobotID: "claimed-bot", Source: "env:OCTO_BOT_TOKEN",
	}
	f.SetConfig(cfg)
	f.SetCredential(bot)
	f.SetClient(client.New(cfg, bot, client.Options{ErrOut: io.Discard, NoRetry: true}))
	_, _, err := execRoot(t, f, "mail", "auth", "login")
	if err == nil || !strings.Contains(err.Error(), "belongs to") {
		t.Fatalf("mail auth login error = %v", err)
	}
}

func TestMailAuthLoginDryRunDoesNotVerifyBotOrStartAuthorization(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	var identityCalls, deviceCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/register":
			identityCalls.Add(1)
			writeTestJSON(w, http.StatusOK, map[string]any{"robot_id": "runtime-bot"})
		case mailDevicePath:
			deviceCalls.Add(1)
			writeTestJSON(w, http.StatusInternalServerError, map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_runtime_token", Format: "json"}
	bot := &credential.BotCredential{
		Token: "app_runtime_token", RobotID: "runtime-bot", SpaceID: "space-runtime",
		Source: "env:OCTO_BOT_TOKEN",
	}
	f.SetConfig(cfg)
	f.SetCredential(bot)

	out, _, err := execRoot(t, f, "mail", "auth", "login", "--dry-run")
	if err != nil {
		t.Fatalf("mail auth login --dry-run: %v\n%s", err, f.ErrOut.String())
	}
	data := dataOf(t, out)
	if data["dry_run"] != true || data["method"] != http.MethodPost ||
		!strings.HasSuffix(data["url"].(string), mailDevicePath) {
		t.Fatalf("dry-run data = %#v", data)
	}
	if identityCalls.Load() != 0 || deviceCalls.Load() != 0 {
		t.Fatalf("identity calls = %d, device calls = %d", identityCalls.Load(), deviceCalls.Load())
	}
	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FindPendingMailAuthorization("runtime-bot", "space-runtime", "app_runtime_token", srv.URL); !errors.Is(err, authstore.ErrMailCredentialNotFound) {
		t.Fatalf("dry-run persisted pending authorization: %v", err)
	}
}

func TestMailAuthStatusDoesNotExchangePendingAuthorizationFromAnotherOrigin(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer srv.Close()

	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	key := mailBindingKey(t, "runtime-bot", "space-runtime", "app_runtime_token", "https://origin-a.example")
	if err := store.SavePendingMailAuthorization(key, &authstore.PendingMailAuthorization{
		DeviceCode: "omd_origin_a", CodeVerifier: "verifier_origin_a",
	}); err != nil {
		t.Fatal(err)
	}

	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_runtime_token", Format: "json"}
	f.SetConfig(cfg)
	f.SetCredential(&credential.BotCredential{
		Token: "app_runtime_token", RobotID: "runtime-bot", SpaceID: "space-runtime",
		Source: "env:OCTO_BOT_TOKEN",
	})

	out, _, err := execRoot(t, f, "mail", "auth", "status")
	if err != nil {
		t.Fatalf("mail auth status: %v\n%s", err, f.ErrOut.String())
	}
	if data := dataOf(t, out); data["status"] != "unconnected" {
		t.Fatalf("status data = %#v", data)
	}
	if calls.Load() != 0 {
		t.Fatalf("origin B received %d request(s) for origin A pending authorization", calls.Load())
	}
}

func TestMailAuthLoginDryRunWithoutBotIDFailsLocally(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_runtime_token", Format: "json"}
	f.SetConfig(cfg)
	f.SetCredential(&credential.BotCredential{
		Token: "app_runtime_token", SpaceID: "space-runtime", Source: "env:OCTO_BOT_TOKEN",
	})

	_, _, err := execRoot(t, f, "mail", "auth", "login", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "requires a resolved Bot id") {
		t.Fatalf("mail auth login --dry-run error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("dry-run made %d network calls", calls.Load())
	}
}

func TestMailAuthStatusDryRunDoesNotVerifyBotOrProbeMailbox(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	var identityCalls, mailboxCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/register":
			identityCalls.Add(1)
			writeTestJSON(w, http.StatusOK, map[string]any{"robot_id": "runtime-bot"})
		case "/agent-mail-api/webapi/v0/identity":
			mailboxCalls.Add(1)
			writeTestJSON(w, http.StatusInternalServerError, map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMailCredential(
		mailBindingKey(t, "runtime-bot", "space-runtime", "app_runtime_token", srv.URL),
		"omb_runtime_mail",
	); err != nil {
		t.Fatal(err)
	}

	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_runtime_token", Format: "json"}
	bot := &credential.BotCredential{
		Token: "app_runtime_token", RobotID: "runtime-bot", Source: "env:OCTO_BOT_TOKEN",
	}
	f.SetConfig(cfg)
	f.SetCredential(bot)

	out, _, err := execRoot(t, f, "mail", "auth", "status", "--dry-run")
	if err != nil {
		t.Fatalf("mail auth status --dry-run: %v\n%s", err, f.ErrOut.String())
	}
	data := dataOf(t, out)
	if data["dry_run"] != true || data["method"] != http.MethodGet ||
		!strings.HasSuffix(data["url"].(string), "/agent-mail-api/webapi/v0/identity") {
		t.Fatalf("dry-run data = %#v", data)
	}
	if identityCalls.Load() != 0 || mailboxCalls.Load() != 0 {
		t.Fatalf("identity calls = %d, mailbox calls = %d", identityCalls.Load(), mailboxCalls.Load())
	}
}

func TestMailAuthRejectsInvalidTargetMailbox(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetCredential(&credential.BotCredential{Token: "app_runtime_token", RobotID: "runtime-bot"})
	_, _, err := execRoot(t, f, "mail", "auth", "login", "--mailbox", "not-an-address")
	if err == nil || !strings.Contains(err.Error(), "invalid Agent Mail mailbox address") {
		t.Fatalf("mail auth login error = %v", err)
	}
}

func TestMailAuthStoredProfileSpaceHintUsesFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/bot/register" {
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
		writeTestJSON(w, http.StatusOK, map[string]any{"robot_id": "runtime-bot"})
	}))
	defer srv.Close()

	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_runtime_token", Format: "json"}
	bot := &credential.BotCredential{
		Token: "app_runtime_token", Profile: "agent", RobotID: "runtime-bot", Source: "profile:agent",
	}
	f.SetConfig(cfg)
	f.SetCredential(bot)
	f.SetClient(client.New(cfg, bot, client.Options{ErrOut: io.Discard, NoRetry: true}))

	_, _, err := execRoot(t, f, "mail", "auth", "login")
	if err == nil || !strings.Contains(err.Error(), "requires a Space") {
		t.Fatalf("mail auth login error = %v", err)
	}
	if trace := f.ErrOut.String(); !strings.Contains(trace, "--space") || strings.Contains(trace, "OCTO_SPACE_ID") {
		t.Fatalf("stored-profile Space hint = %s", trace)
	}
}

func TestMailAuthStatusProbeHonorsVerboseAndNoRetry(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	var identityCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/register":
			writeTestJSON(w, http.StatusOK, map[string]any{"robot_id": "robot-1"})
		case "/agent-mail-api/webapi/v0/identity":
			identityCalls.Add(1)
			writeTestJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]any{"code": "unavailable", "message": "try later"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMailCredential(
		mailBindingKey(t, "robot-1", "space-1", "app_bot_token", srv.URL),
		"omb_bound_secret",
	); err != nil {
		t.Fatal(err)
	}
	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_bot_token", Format: "json"}
	bot := &credential.BotCredential{
		Token: "app_bot_token", Profile: "agent", RobotID: "robot-1", SpaceID: "space-1", Source: "profile:agent",
	}
	f.SetConfig(cfg)
	f.SetCredential(bot)
	f.SetClient(client.New(cfg, bot, client.Options{ErrOut: io.Discard, NoRetry: true}))

	_, _, err = execRoot(t, f, "mail", "auth", "status", "--profile", "agent", "--verbose", "--no-retry", "--timeout", "1s")
	if err == nil {
		t.Fatal("mail auth status: expected identity probe error")
	}
	if got := identityCalls.Load(); got != 1 {
		t.Fatalf("identity probe calls = %d, want 1 with --no-retry", got)
	}
	if trace := f.ErrOut.String(); !strings.Contains(trace, "/agent-mail-api/webapi/v0/identity") {
		t.Fatalf("verbose status trace omitted identity probe: %s", trace)
	}
}

func TestMailAuthStatusProbeHonorsTimeout(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/register":
			writeTestJSON(w, http.StatusOK, map[string]any{"robot_id": "robot-1"})
		case "/agent-mail-api/webapi/v0/identity":
			time.Sleep(100 * time.Millisecond)
			writeTestJSON(w, http.StatusOK, map[string]any{"address": "agent@example.com"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMailCredential(
		mailBindingKey(t, "robot-1", "space-1", "app_bot_token", srv.URL),
		"omb_bound_secret",
	); err != nil {
		t.Fatal(err)
	}
	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "app_bot_token", Format: "json"}
	bot := &credential.BotCredential{
		Token: "app_bot_token", Profile: "agent", RobotID: "robot-1", SpaceID: "space-1", Source: "profile:agent",
	}
	f.SetConfig(cfg)
	f.SetCredential(bot)
	f.SetClient(client.New(cfg, bot, client.Options{ErrOut: io.Discard, NoRetry: true}))

	_, _, err = execRoot(t, f, "mail", "auth", "status", "--profile", "agent", "--no-retry", "--timeout", "10ms")
	if err == nil {
		t.Fatal("mail auth status: identity probe ignored --timeout")
	}
}

func writeTestJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
