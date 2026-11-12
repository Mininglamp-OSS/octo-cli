package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOwnedSpoolPreservesSourceJSONExpansion(t *testing.T) {
	body, err := json.Marshal(map[string]string{"text": strings.Repeat("<", 3<<20)})
	if err != nil {
		t.Fatal(err)
	}
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actual, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(actual, body) {
			t.Error("sealed source bytes changed", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer receiver.Close()
	p := fixture(t)
	p.Key.Origin = receiver.URL
	raw := encode(t, p)
	c, i := testCoordinator(t, p, policyFunc(func(context.Context, Key) ([]byte, error) { return raw, nil }), factFunc(allFacts))
	conf := receiver.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	conf.InsecureSkipVerify = false
	action, err := PrepareHTTP(context.Background(), p.Key, "POST", "/source", nil, body, conf, time.Now().Add(15*time.Second))
	if err != nil {
		t.Fatal("source-valid escaped JSON rejected by managed serialized spool", err)
	}
	result, err := c.Execute(context.Background(), i, action, testCap)
	if err != nil {
		t.Fatal(err)
	}
	_ = result.Response.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, receiver.URL+"/roundtrip", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	next, err := prepareRequest(ctx, p.Key, req, conf)
	if err != nil {
		t.Fatal("source-valid escaped JSON rejected by roundtripper spool", err)
	}
	result, err = c.Execute(ctx, i, next, testCap)
	if err != nil {
		t.Fatal(err)
	}
	_ = result.Response.Body.Close()
}
