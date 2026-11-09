package permission

import (
	"context"
	"testing"
	"time"
)

func TestContendedScopeWaitObservesRequestAndTurnCancellation(t *testing.T) {
	for _, turn := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "turn"}[turn], func(t *testing.T) {
			p := fixture(t)
			raw := encode(t, p)
			read := make(chan struct{}, 1)
			c, i := testCoordinator(t, p, policyFunc(func(context.Context, Key) ([]byte, error) {
				read <- struct{}{}
				return raw, nil
			}), factFunc(allFacts))
			s := c.scope(&i.binding)
			if !s.TryLock() {
				t.Fatal("fixture scope not free")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out := make(chan error, 1)
			go func() {
				out <- c.atBoundary(ctx, i, testCap, func() error {
					t.Error("cancelled waiter dispatched")
					return nil
				})
			}()
			<-read
			want := "policy_unavailable"
			if turn {
				i.Close()
				want = "context_untrusted"
			} else {
				cancel()
			}
			select {
			case err := <-out:
				s.Unlock()
				category(t, err, want)
			case <-time.After(time.Second):
				// Release and collect even on the old implementation's failure.
				s.Unlock()
				<-out
				t.Fatal("cancelled waiter remained blocked behind scope owner")
			}
		})
	}
}
