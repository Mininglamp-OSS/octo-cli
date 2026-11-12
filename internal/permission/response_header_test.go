package permission

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOwnedHTTPResponseRetainsOrdinaryHeaderBudgetWithoutCappingBody(t *testing.T) {
	p := fixture(t)
	raw := encode(t, p)
	c, i := testCoordinator(t, p, policyFunc(func(context.Context, Key) ([]byte, error) { return raw, nil }), factFunc(allFacts))
	for _, oversizedHeader := range []bool{false, true} {
		t.Run(map[bool]string{false: "large_body", true: "oversized_header"}[oversizedHeader], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return
				}
				var request [1]byte
				if _, err := io.ReadFull(conn, request[:]); err != nil {
					return
				}
				if oversizedHeader {
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nX-Synthetic: "+strings.Repeat("a", (10<<20)+1)+"\r\nContent-Length: 0\r\n\r\n")
				} else {
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 12582912\r\n\r\n"+strings.Repeat("b", 12<<20))
				}
			}()
			conn, err := net.Dial("tcp", listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			prepared := &PreparedHTTP{key: p.Key, conn: conn, request: []byte("x"), method: http.MethodGet, wire: &wireCapture{Conn: conn}}
			result, err := c.Execute(ctx, i, prepared, testCap)
			if err == nil {
				if result.Response == nil {
					t.Fatal("missing response")
				}
				n, readErr := io.Copy(io.Discard, result.Response.Body)
				_ = result.Response.Body.Close()
				<-done
				if oversizedHeader {
					t.Fatal("owned parser accepted headers beyond ordinary net/http 10MiB budget")
				}
				if readErr != nil || n != 12<<20 {
					t.Fatal("header budget became a response-body cap", n, readErr)
				}
			} else {
				<-done
				if !oversizedHeader || result.Status != "result_unknown" {
					t.Fatal("unexpected physical attempt result", result.Status, err)
				}
			}
		})
	}
}
