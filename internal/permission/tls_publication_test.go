package permission

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOwnedTLSResponsePublishesOrReturnsReadableRefusal(t *testing.T) {
	requireOwnedPlatform(t)
	for _, deny := range []bool{false, true} {
		t.Run(fmt.Sprint(deny), func(t *testing.T) {
			f := newRuntimeFixture(t, testCap)
			template := httptest.NewTLSServer(http.NotFoundHandler())
			cert := template.TLS.Certificates[0]
			client := template.Client()
			template.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			secured := PublicationTLSListener(listener, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})
			frame := func(text string) []byte {
				return []byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(text), text))
			}
			server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				ctx, err := f.c.BeginCall(ctx, f.i, testCap, nil, nil)
				if err != nil {
					t.Error(err)
					return
				}
				call := BoundCall(ctx)
				defer call.Close()
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if deny {
					f.deny()
				}
				err = call.PublishConnection(ctx, conn, frame("protected-result"))
				if deny {
					category(t, err, "capability_denied")
					if _, err = conn.Write(frame("fixed-refusal")); err != nil {
						t.Error(err)
					}
				} else if err != nil {
					t.Error(err)
				}
			})}
			done := make(chan error, 1)
			go func() { done <- server.Serve(secured) }()
			defer func() { server.Close(); secured.Close(); <-done }()
			response, err := client.Get("https://" + listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := "protected-result"
			if deny {
				want = "fixed-refusal"
			}
			if string(body) != want {
				t.Fatal(string(body), want)
			}
		})
	}
}
