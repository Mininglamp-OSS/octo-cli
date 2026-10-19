package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// A census over the secret-redaction surface, written in answer to round-16's process
// finding rather than to any single defect.
//
// Every round of review on this branch has closed one redaction hole and surfaced the next
// one in a different corner of the same mechanism: the share-URL rebuild, the retry trace,
// --jq, pflag parse errors, `api` path parameters, `api` body parameters, and in round 16
// the value's JSON *type*, a non-JSON body, and a JSON prefix. Each of those is one cell of
// a table — (JSON kind × position in the body × output surface) — and each was found by a
// human reading code rather than by a test. Enumerating the table is the only thing that
// makes the sequence terminate: a new cell either fails here or it does not exist.
//
// What is and is not covered is stated deliberately, because a census that overstates its
// reach is worse than none. Covered: the three JSON kinds a scalar can be (string, number,
// boolean), five positions (top-level value, nested value, array element, array-of-object
// value, object key), and the surfaces that print a body or an error. Not covered here:
// query and header position, which no embedded spec declares secret today and which
// `api` cannot collect — that gap is pinned by
// TestSecrets_NoSpecDeclaresASecretApiCannotCollectOrMask in package cmd, not by this file.

// censusKinds are the JSON kinds a declared secret can arrive as. Each carries its wire
// spelling, because that — not the Go type — is what a reader of the envelope sees.
var censusKinds = []struct {
	name    string
	secret  string
	encoded string // the value's spelling inside a JSON document
	goValue any    // the same value as a decoded Go leaf
}{
	{"a long string", "S3CR3TP4SSW0RD", `"S3CR3TP4SSW0RD"`, "S3CR3TP4SSW0RD"},
	// Short strings are their own kind in practice: the boundary rule in replaceSecretForm
	// declines them unless delimited, so every short-secret hole so far has been a place
	// that asked the masker instead of asking about disclosure.
	{"a short string", "pw12345", `"pw12345x"`, "pw12345x"},
	{"a number", "90071992547409931", `90071992547409931`, json.Number("90071992547409931")},
	{"a short number", "8675309", `8675309`, json.Number("8675309")},
	{"a boolean", "true", `true`, true},
}

// censusBodies place one encoded value in each position a secret has been found in.
var censusBodies = []struct {
	name string
	body func(encoded string) string
}{
	{"a top-level value", func(e string) string { return `{"password":` + e + `}` }},
	{"a nested value", func(e string) string { return `{"detail":{"password":` + e + `}}` }},
	{"an array element", func(e string) string { return `{"detail":{"list":[` + e + `]}}` }},
	{"a value inside an array of objects", func(e string) string {
		return `{"detail":[{"password":` + e + `}]}`
	}},
	{"a deeply nested value", func(e string) string {
		return `{"a":{"b":{"c":[{"password":` + e + `}]}}}`
	}},
}

// TestSecretCensus_ResponseBodiesNeverDiscloseADeclaredSecret walks kind × position over the
// response-side entry point. This is where round-16's P1-3 (a secret masked only as a
// string) and P1-4 (a JSON prefix accepted as the whole body) both lived.
func TestSecretCensus_ResponseBodiesNeverDiscloseADeclaredSecret(t *testing.T) {
	for _, kind := range censusKinds {
		for _, pos := range censusBodies {
			t.Run(kind.name+"/"+pos.name, func(t *testing.T) {
				body := pos.body(kind.encoded)
				got := string(redactResponseBody([]byte(body), []string{kind.secret}))
				if strings.Contains(got, kind.secret) {
					t.Errorf("redactResponseBody(%s) = %s — the secret survived", body, got)
				}
			})
		}
	}
}

// TestSecretCensus_ObjectKeysNeverDiscloseADeclaredSecret is the key position, split out
// because only a string can be a JSON key. Both the bare key and a key with an adjacent
// character are covered: the second is the shape that defeats the boundary rule, and it is
// the one that was open until this round.
func TestSecretCensus_ObjectKeysNeverDiscloseADeclaredSecret(t *testing.T) {
	for _, secret := range []string{"S3CR3TP4SSW0RD", "pw12345", "1234", "pw"} {
		for _, key := range []string{secret, secret + "x", "x" + secret, "id-" + secret} {
			t.Run(secret+"/"+key, func(t *testing.T) {
				body := `{"detail":{"` + key + `":"per-id result"}}`
				got := string(redactResponseBody([]byte(body), []string{secret}))
				if strings.Contains(got, secret) {
					t.Errorf("redactResponseBody(%s) = %s — the secret survived in a key", body, got)
				}
			})
		}
	}
}

// TestSecretCensus_RequestTracesNeverDiscloseADeclaredSecret is the request-side half:
// redactBodyForLog is what --verbose and --dry-run print. `api` refuses a non-string value at
// a declared-secret property, so a number should not reach here from that command — but the
// masker is shared with every generated command and with any future caller, so it is held to
// the same rule rather than to that assumption.
func TestSecretCensus_RequestTracesNeverDiscloseADeclaredSecret(t *testing.T) {
	for _, kind := range censusKinds {
		for _, shape := range []struct {
			name string
			body any
		}{
			{"a top-level value", map[string]any{"password": kind.goValue}},
			{"a nested value", map[string]any{"outer": map[string]any{"password": kind.goValue}}},
			{"an array element", map[string]any{"list": []any{kind.goValue}}},
			{"a value inside an array of objects", map[string]any{
				"list": []any{map[string]any{"password": kind.goValue}},
			}},
			{"a caller-supplied key", map[string]any{fmt.Sprintf("%v", kind.goValue): "v"}},
			// The shape cmd/service/aliases.go builds, which redactBodyValue's default arm
			// exists for. Only string leaves are representable here.
			{"a map[string]string value", map[string]string{"password": fmt.Sprintf("%v", kind.goValue)}},
			{"a map[string]string key", map[string]string{fmt.Sprintf("%v", kind.goValue): "v"}},
		} {
			t.Run(kind.name+"/"+shape.name, func(t *testing.T) {
				marshalled, err := json.Marshal(shape.body)
				if err != nil {
					t.Fatalf("marshal the fixture: %v", err)
				}
				req := &Request{Body: shape.body, SecretValues: []string{kind.secret}}
				got := redactBodyForLog(req, marshalled)
				if strings.Contains(got, kind.secret) {
					t.Errorf("redactBodyForLog(%s) = %s — the secret survived", marshalled, got)
				}
			})
		}
	}
}

// TestSecretCensus_EveryPrintingSurfaceIsCovered drives the real client once per surface that
// can print a secret, with a short secret in both path and body position — short, because
// that is the length every hole so far has needed.
//
// The surfaces are enumerated from the code rather than assumed: --verbose request trace,
// --dry-run description, a JSON backend error body, a non-JSON backend error body, and the
// retry trace, which prints once per attempt.
func TestSecretCensus_EveryPrintingSurfaceIsCovered(t *testing.T) {
	const password = "pw12345"
	const token = "tok4567"

	// The echo shape that defeats the boundary rule, reused across the body-bearing surfaces.
	echo := func(mediaType, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if mediaType != "" {
				w.Header().Set("Content-Type", mediaType)
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(body))
		}))
	}

	for _, tc := range []struct {
		name    string
		run     func(t *testing.T) string // returns everything the surface printed
		mustSay bool                      // the surface must show a mask, not just hide the value
	}{
		{
			name: "the verbose request trace",
			run: func(t *testing.T) string {
				srv := echo("application/json", `{"error":"wrong_password","message":"no"}`)
				defer srv.Close()
				var errOut bytes.Buffer
				c := New(&config.Config{APIBaseURL: srv.URL}, &credential.BotCredential{Token: "uk_t"},
					Options{Verbose: true, NoRetry: true, ErrOut: &errOut})
				_, _ = c.Do(context.Background(), censusRequest(token, password))
				return errOut.String()
			},
			mustSay: true,
		},
		{
			name: "the dry-run description",
			run: func(t *testing.T) string {
				c := New(&config.Config{APIBaseURL: "https://octo.test"},
					&credential.BotCredential{Token: "uk_t"},
					Options{DryRun: true, ErrOut: io.Discard})
				body, err := c.Do(context.Background(), censusRequest(token, password))
				if err != nil {
					t.Fatalf("dry run: %v", err)
				}
				return string(body)
			},
			mustSay: true,
		},
		{
			name: "a JSON backend error body",
			run: func(t *testing.T) string {
				srv := echo("application/json",
					`{"error":"wrong_password","message":"bad password `+password+`x","detail":{"`+token+`x":1}}`)
				defer srv.Close()
				var errOut bytes.Buffer
				c := New(&config.Config{APIBaseURL: srv.URL}, &credential.BotCredential{Token: "uk_t"},
					Options{NoRetry: true, ErrOut: &errOut})
				_, err := c.Do(context.Background(), censusRequest(token, password))
				return censusErrorText(t, err) + errOut.String()
			},
		},
		{
			name: "a non-JSON backend error body",
			run: func(t *testing.T) string {
				srv := echo("text/html",
					`<html>blocked by WAF: password=`+password+`x token=`+token+`x</html>`)
				defer srv.Close()
				var errOut bytes.Buffer
				c := New(&config.Config{APIBaseURL: srv.URL}, &credential.BotCredential{Token: "uk_t"},
					Options{NoRetry: true, ErrOut: &errOut})
				_, err := c.Do(context.Background(), censusRequest(token, password))
				return censusErrorText(t, err) + errOut.String()
			},
		},
		{
			name: "the retry trace, which prints once per attempt",
			run: func(t *testing.T) string {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":"internal","message":"try later ` + password + `x"}`))
				}))
				defer srv.Close()
				var errOut bytes.Buffer
				c := New(&config.Config{APIBaseURL: srv.URL}, &credential.BotCredential{Token: "uk_t"},
					Options{Verbose: true, ErrOut: &errOut, Timeout: "5s"})
				_, err := c.Do(context.Background(), censusRequest(token, password))
				return censusErrorText(t, err) + errOut.String()
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			printed := tc.run(t)
			for _, secret := range []string{password, token} {
				if strings.Contains(printed, secret) {
					t.Errorf("%s disclosed %q:\n%s", tc.name, secret, printed)
				}
			}
			if tc.mustSay && !strings.Contains(printed, secretMask) {
				t.Errorf("%s printed no mask at all, so this row would pass even if the "+
					"surface stopped printing the body:\n%s", tc.name, printed)
			}
		})
	}
}

// censusRequest is one request carrying a declared secret in both positions a spec can mark.
func censusRequest(token, password string) *Request {
	return &Request{
		Method:       http.MethodPost,
		Path:         "/v1/user/drive/shares/" + token + "/access",
		Body:         map[string]any{"password": password},
		SecretValues: []string{token, password},
	}
}

// censusErrorText renders everything an error would show a caller: the message, the hint and
// the detail payload.
func censusErrorText(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error from this surface")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		return err.Error()
	}
	return strings.Join([]string{err.Error(), ee.Code, ee.Message, ee.Hint, string(ee.Detail)}, "\n")
}
