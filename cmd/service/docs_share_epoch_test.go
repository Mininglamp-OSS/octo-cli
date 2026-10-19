package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestDocsSharePermissionEpochDiscovery(t *testing.T) {
	reg := registry.MustNew()
	for _, id := range []string{"docs.share.get", "docs.share.set"} {
		op, ok := reg.GetOperation(id)
		if !ok || op.ResponseSchema == nil {
			t.Fatalf("%s response schema missing", id)
		}
		epoch, ok := op.ResponseSchema.Properties["permissionEpoch"]
		if !ok || epoch.Type != "integer" {
			t.Fatalf("%s response must expose integer permissionEpoch: %#v", id, epoch)
		}
	}
}

func TestDocsSharePermissionEpochRequired(t *testing.T) {
	calls := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{}`)) })
	root.SetArgs([]string{"docs", "share", "set", "d1", "--scope", "restricted"})
	if err := root.Execute(); err == nil || calls != 0 {
		t.Fatalf("missing permissionEpoch must fail before HTTP: calls=%d err=%v", calls, err)
	}
}

func TestDocsSharePermissionEpochWire(t *testing.T) {
	for _, args := range [][]string{
		{"--scope", "restricted", "--permissionEpoch", "0"},
		{"--scope", "anyone_in_space", "--role", "edit", "--permissionEpoch", "12"},
		{"--data", `{"shareScope":"restricted","permissionEpoch":12}`},
	} {
		t.Run(args[0]+args[len(args)-1], func(t *testing.T) {
			calls := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if _, ok := body["permissionEpoch"].(float64); !ok {
					t.Fatalf("numeric permissionEpoch missing: %v", body)
				}
				want := float64(12)
				if args[len(args)-1] == "0" {
					want = 0
				}
				if body["permissionEpoch"] != want {
					t.Fatalf("epoch changed: got %v, want %v", body["permissionEpoch"], want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"permissionEpoch":13}`))
			})
			root.SetArgs(append([]string{"docs", "share", "set", "d1"}, args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("expected one write, got %d", calls)
			}
		})
	}
}

func TestDocsSharePermissionConflictIsNotRetried(t *testing.T) {
	calls := 0
	root, out, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"share_settings_conflict","current":{"permissionEpoch":13,"shareScope":"restricted"}}`))
	})
	root.SetArgs([]string{"docs", "share", "set", "d1", "--scope", "restricted", "--permissionEpoch", "12"})
	if err := root.Execute(); err == nil || calls != 1 {
		t.Fatalf("conflict must be returned without retry: calls=%d err=%v", calls, err)
	}
	if !strings.Contains(out.ErrOut.String(), "share_settings_conflict") {
		t.Fatalf("backend conflict lost: %s", out.ErrOut.String())
	}
}

func TestDocsSharePermissionEpochInvalid(t *testing.T) {
	for _, value := range []string{"null", "-1", "1.5", `"12"`, "9007199254740992"} {
		t.Run(value, func(t *testing.T) {
			calls := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
			root.SetArgs([]string{"docs", "share", "set", "d1", "--data", `{"shareScope":"restricted","permissionEpoch":` + value + `}`})
			if err := root.Execute(); err == nil || calls != 0 {
				t.Fatalf("invalid epoch must fail before HTTP: calls=%d err=%v", calls, err)
			}
		})
	}
}
