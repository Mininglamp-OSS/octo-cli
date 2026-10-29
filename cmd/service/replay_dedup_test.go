package service

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Carrying an idempotency key does not make a write replay-safe. Only an
// operation whose backend deduplicates every resend, called with a key, may be
// resent after an ambiguous failure; every other keyed operation is sent once.
func TestReplayDeduplicatedRequiresAVerifiedContract(t *testing.T) {
	reg := registry.MustNew()
	for _, c := range []struct {
		id      string
		headers map[string]string
		body    map[string]any
		want    bool
	}{
		{"capability.install", map[string]string{"Idempotency-Key": "k"}, nil, true},
		{"capability.install", map[string]string{"Idempotency-Key": "  "}, nil, false},
		{"capability.install", nil, nil, false},
		// docs-backend reads the key on html_ppt paths only.
		{"docs.create", map[string]string{"Idempotency-Key": "k"}, map[string]any{"docType": "html_ppt"}, false},
		{"docs.comments.add", map[string]string{"Idempotency-Key": "k"}, nil, false},
		{"docs.versions.create", map[string]string{"Idempotency-Key": "k"}, nil, false},
		{"docs.versions.restore", map[string]string{"Idempotency-Key": "k"}, nil, false},
		// Backend contract not verified.
		{"summary.create", map[string]string{"Idempotency-Key": "k"}, nil, false},
		{"html.publish", nil, map[string]any{"idempotency_key": "k"}, false},
		{"html.draft.create", nil, map[string]any{"idempotency_key": "k"}, false},
	} {
		detail, ok := reg.GetOperation(c.id)
		if !ok {
			t.Fatalf("operation %s is not embedded", c.id)
		}
		req := &client.Request{Method: detail.Method, Headers: c.headers}
		if c.body != nil {
			req.Body = c.body
		}
		if got := replayDeduplicated(&operationRuntime{detail: detail}, req); got != c.want {
			t.Errorf("%s headers=%v body=%v: replay-safe = %v, want %v", c.id, c.headers, c.body, got, c.want)
		}
	}
}
