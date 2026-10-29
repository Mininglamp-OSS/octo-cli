package cmd

import "testing"

// TestNewMCPHTTPServer_SetsConnectionTimeouts pins the slow/idle-client bounds
// on the MCP HTTP server so a future edit that drops them fails CI.
func TestNewMCPHTTPServer_SetsConnectionTimeouts(t *testing.T) {
	srv := newMCPHTTPServer(":0", nil)
	if srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout must be set")
	}
	if srv.ReadTimeout == 0 {
		t.Error("ReadTimeout must be set (bounds a slow-drip body before dispatch)")
	}
	if srv.IdleTimeout == 0 {
		t.Error("IdleTimeout must be set (bounds an idle kept-alive connection)")
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,
		"0.0.0.0:8080":   false,
		":8080":          false, // bare port binds every interface
		"10.0.0.5:8080":  false,
	}
	for addr, want := range cases {
		if got := isLoopbackAddr(addr); got != want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}
