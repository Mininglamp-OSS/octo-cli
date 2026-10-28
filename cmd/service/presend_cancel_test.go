package service

import (
	"context"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestXIN2549CLIContext(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "expired"}[expired], func(t *testing.T) {
			var hits atomic.Int32
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte(`{}`)) })
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if expired {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer stop()
			}
			root.SetArgs([]string{"message", "send", "--data", `{"channel_id":"c1","channel_type":1,"payload":{"text":"inert"}}`})
			err := root.ExecuteContext(ctx)
			ee := output.AsExitError(err)
			if ee == nil {
				t.Fatal("expected classified failure")
			}
			t.Logf("deliveries=%d code=%s not_processed=%t", hits.Load(), ee.Code, ee.NotProcessed)
			if hits.Load() != 0 || ee.Code == "RESULT_UNKNOWN" || !ee.NotProcessed {
				t.Error("unsent CLI mutation must not report RESULT_UNKNOWN")
			}
		})
	}
}
