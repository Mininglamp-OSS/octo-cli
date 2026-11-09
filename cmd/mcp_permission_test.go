package cmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/mcp"
	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

type commandPermissionPolicy []byte

func (p commandPermissionPolicy) ReadBundle(context.Context, permission.Key) ([]byte, error) {
	return append([]byte(nil), p...), nil
}

type commandPermissionFacts struct{ conf *tls.Config }

func (f commandPermissionFacts) TLSConfiguration() *tls.Config { return f.conf.Clone() }
func (f commandPermissionFacts) Recheck(_ context.Context, b permission.Binding, _ permission.Needs) (permission.Facts, error) { //nolint:gocritic // detached private fixture follows the binding interface
	return permission.Facts{Subject: b.Subject, Complete: true, InvocationEligible: true, CapabilityEligible: true}, nil
}

// The actual command tree must traverse scene.get under its own capability,
// even though the logical portable export root is allowed in both cases.
func TestManagedPortableCommandPositiveAndChildDenial(t *testing.T) {
	for _, denyChild := range []bool{false, true} {
		t.Run(map[bool]string{false: "allow", true: "deny_child"}[denyChild], func(t *testing.T) {
			var reads atomic.Int32
			backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/auth/verify-bot":
					w.Write([]byte(`{"identity_version":2,"principal_kind":"user_bot","bot_uid":"bot","mcp_policy":{"version":1,"context_mode":"compatible","override":null,"stage_default":"compatible","source":"stage_default","generation":"1","bot_revision":null}}`))
				case "/v1/bot/docs/board/scene":
					reads.Add(1)
					w.Write([]byte(`{"elements":[],"files":{},"appState":{},"baseVersion":"synthetic-version"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer backend.Close()
			t.Setenv("OCTO_API_BASE_URL", backend.URL)
			t.Setenv("OCTO_BOT_TOKEN", "bf_synthetic")
			caps := []string{"octo-cli/docs/op/docs.scene.export", "octo-cli/docs/op/docs.scene.get", "octo-cli/docs/op/docs.attachments.get", "octo-cli/docs/derived/portable-attachment-bytes"}
			allowed := caps
			if denyChild {
				allowed = caps[:1]
			}
			key := permission.Key{Origin: backend.URL, BotUID: "bot", SpaceID: "space"}
			stage := func(capability bool) permission.Stage {
				r := permission.Rule{"rule_id": "allow", "effect": "allow", "caller": map[string]any{"kind": "all_humans"}, "source": map[string]any{"kind": "all_in_space", "conversation_types": []any{"group"}}}
				if capability {
					r["capabilities"] = allowed
				}
				return permission.Stage{State: "E", Rules: []permission.Rule{r}}
			}
			bundle := permission.Bundle{SchemaVersion: 1, Key: key, Incarnation: strings.Repeat("1", 32), Revision: "0", ContextEpoch: "0", CatalogVersion: "synthetic", CatalogDigest: strings.Repeat("a", 64), Invocation: stage(false), Capability: stage(true)}
			_, raw, err := permission.Seal(bundle)
			if err != nil {
				t.Fatal(err)
			}
			conf := backend.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			conf.InsecureSkipVerify = false
			coordinator, err := permission.NewCoordinator(permission.Catalog{Version: bundle.CatalogVersion, Digest: bundle.CatalogDigest, Capabilities: caps}, commandPermissionPolicy(raw), commandPermissionFacts{conf})
			if err != nil {
				t.Fatal(err)
			}
			inv, err := coordinator.Register(permission.Binding{Key: key, Incarnation: bundle.Incarnation, ContextEpoch: "0", Generation: "synthetic", Subject: permission.Subject{Caller: "alice", Kind: "group", Source: "group"}})
			if err != nil {
				t.Fatal(err)
			}
			defer inv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err = coordinator.Admit(ctx, inv); err != nil {
				t.Fatal(err)
			}
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			logs, err := os.MkdirTemp(cwd, ".permission-command-log-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(logs)
			admission, err := mcp.NewAdmission(logs, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer admission.Close()
			s, err := mcp.NewServer(NewRootCmd)
			if err != nil {
				t.Fatal(err)
			}
			s.WithManagedTurn(mcp.NewManagedTurn(coordinator, inv, permission.DeclarationV2{Sender: "alice", Channel: "group", ChannelType: 2, SpaceID: "space", MessageID: "42"})).WithAdmission(admission).WithTrustedContext(mcp.TrustedContext{SpaceID: "space"})
			facade, _ := mcp.ParseFacade("both")
			s.WithFacade(facade)
			input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"experimental":{"octo_permission_binding":{"selected_binding":2,"selected_metadata":2,"selected_identity":2}}}}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"execute","arguments":{"operation_id":"docs.scene.export","arguments":{"docId":"board","format":"excalidraw"}},"_meta":{"octo":{"version":2,"im":{"sender":"alice","channel":"group","channel_type":2,"space_id":"space","message_id":"42"}}}}}` + "\n"
			var out bytes.Buffer
			if err = s.ServeStdio(ctx, strings.NewReader(input), &out); err != nil {
				t.Fatal(err)
			}
			lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'})
			if len(lines) != 2 {
				t.Fatal(out.String())
			}
			var response struct {
				Error  *struct{ Code int }
				Result json.RawMessage
			}
			if json.Unmarshal(lines[1], &response) != nil {
				t.Fatal(out.String())
			}
			if denyChild {
				if response.Error == nil || response.Error.Code != -32007 || reads.Load() != 0 {
					t.Fatal("child borrowed root", out.String(), reads.Load())
				}
				return
			}
			if response.Error != nil || reads.Load() != 1 || bytes.Contains(response.Result, []byte(`"isError":true`)) || !bytes.Contains(response.Result, []byte("synthetic-version")) {
				t.Fatal("portable positive", out.String(), reads.Load())
			}
		})
	}
}
