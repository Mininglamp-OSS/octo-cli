package client

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestXIN2549ArrayRedaction(t *testing.T) {
	secret := "pw12345"
	for _, shape := range []string{"typed", "any", "nested-typed", "nested-any"} {
		for _, projection := range []string{"verbose", "dry-run", "error-body"} {
			t.Run(shape+"/"+projection, func(t *testing.T) {
				var arr any = []any{secret + "x", "public-value"}
				if strings.Contains(shape, "typed") {
					arr = []string{secret + "x", "public-value"}
				}
				var body any = map[string]any{"values": arr}
				if strings.HasPrefix(shape, "nested") {
					body = map[string]any{"outer": map[string]any{"values": arr}}
				}
				raw, _ := json.Marshal(body)
				var hits atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					received, _ := io.ReadAll(r.Body)
					if !bytes.Equal(received, raw) {
						t.Error("diagnostic masking changed outbound bytes")
					}
					w.WriteHeader(400)
					w.Write(raw)
				}))
				defer srv.Close()
				c := newTestClient(srv)
				var trace bytes.Buffer
				c.options.ErrOut = &trace
				c.options.Verbose = projection == "verbose"
				c.options.DryRun = projection == "dry-run"
				out, err := c.Do(context.Background(), &Request{Method: "POST", Path: "/fixture", Body: body, SecretValues: []string{secret}})
				var rendered string
				switch projection {
				case "verbose":
					rendered = trace.String()
				case "dry-run":
					rendered = string(out)
				case "error-body":
					if err == nil {
						t.Fatal("missing backend error")
					}
					b, _ := json.Marshal(output.AsExitError(err))
					rendered = string(b)
				}
				leaked := strings.Contains(rendered, secret)
				t.Logf("synthetic_canary_present=%t deliveries=%d", leaked, hits.Load())
				wantHits := int32(1)
				if projection == "dry-run" {
					wantHits = 0
				}
				if hits.Load() != wantHits || !strings.Contains(rendered, "public-value") {
					t.Error("delivery count or non-secret diagnostic changed")
				}
				unchanged, _ := json.Marshal(body)
				if !bytes.Equal(unchanged, raw) {
					t.Error("masking mutated caller body")
				}
				if leaked {
					t.Error("suppression-aware masking invariant violated; raw diagnostic withheld")
				}
			})
		}
	}
}
