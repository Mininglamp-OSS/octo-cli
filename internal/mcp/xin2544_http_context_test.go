package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// Even noncanonical Header maps supplied by an embedding handler must not
// promote caller input to trusted provenance. Wire-level cases use the binary.
func TestXIN2544HTTPContextHeadersFailClosed(t *testing.T) {
	for _, tc := range []TrustedContext{{}, {SpaceID: "operator-space", ChannelID: "operator-channel", ChannelType: "1", OnBehalfOf: "operator-person"}} {
		h, err := NewHTTPHandler(testRoot, tc, cmdutil.GlobalOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{headerSpaceID, headerChannelID, headerChannelType, headerOnBehalfOf, "x-sPaCe-iD", "X-ſpace-Id"} {
			for _, values := range [][]string{{"attacker"}, {""}, {"first", "second"}, nil} {
				r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
				r.Header.Set("Authorization", "Bearer bf_same_caller")
				r.Header[key] = values
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden {
					t.Errorf("key=%q values=%v forced=%v: status=%d, want 403", key, values, tc.SpaceID != "", w.Code)
				}
				if strings.Contains(w.Body.String(), "operator-") || strings.Contains(w.Body.String(), "attacker") {
					t.Errorf("refusal disclosed context values: %s", w.Body)
				}
				if got := h.connectionServer(r).trusted; got != tc {
					t.Errorf("key=%q: trusted context=%+v, want operator snapshot %+v", key, got, tc)
				}
			}
		}
	}
}
