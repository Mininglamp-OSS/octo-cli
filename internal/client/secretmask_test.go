package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// escapedSecrets are the values that defeated the old mask. redactSecrets did a
// literal strings.ReplaceAll over the *already-marshalled* body, and
// json.Marshal rewrites `"` to `\"`, `\` to `\\` and a newline to `\n` first —
// so the literal was no longer present and the substitution found nothing to
// replace. The mask silently no-oped on exactly the passwords a user is most
// likely to choose, and because the transport retries, the line was emitted once
// per attempt.
var escapedSecrets = []struct {
	name  string
	value string
}{
	{"double quote", `p@ss"word`},
	{"backslash", `p@ss\word`},
	{"quote and backslash", `p@ss"w\ord`},
	{"newline", "p@ss\nword"},
	{"tab and control char", "p@ss\tword\x01"},
	{"unicode escape territory", "p@ss\u2028word"},
	{"escape-free control", `plain-hunter2`},
}

// TestSecretValues_JSONEscapingDoesNotDefeatTheMask asserts a password whose
// JSON encoding differs from its literal is still masked in the verbose trace,
// and still reaches the wire intact.
func TestSecretValues_JSONEscapingDoesNotDefeatTheMask(t *testing.T) {
	for _, tc := range escapedSecrets {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			var trace bytes.Buffer
			c := New(
				&config.Config{APIBaseURL: srv.URL},
				&credential.BotCredential{Token: "bf_t"},
				Options{Verbose: true, ErrOut: &trace},
			)
			if _, err := c.Do(context.Background(), &Request{
				Method:       http.MethodPost,
				Path:         "/v1/bot/drive/shares",
				Body:         map[string]any{"file_id": json.Number("1"), "password": tc.value},
				SecretValues: []string{tc.value},
			}); err != nil {
				t.Fatalf("Do: %v", err)
			}

			// The wire keeps the real value; redaction is a logging concern.
			var sent map[string]any
			if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
				t.Fatalf("parse sent body %q: %v", gotBody, err)
			}
			if sent["password"] != tc.value {
				t.Errorf("wire password: got %q, want %q", sent["password"], tc.value)
			}

			assertTraceMasks(t, trace.String(), tc.value)
		})
	}
}

// TestSecretValues_JSONEscapingDoesNotDefeatTheDryRunMask is the same property
// on the other surface. A dry-run description is meant to be safe to paste into
// a ticket, so it is the one a masked-but-not-really value hurts most.
func TestSecretValues_JSONEscapingDoesNotDefeatTheDryRunMask(t *testing.T) {
	for _, tc := range escapedSecrets {
		t.Run(tc.name, func(t *testing.T) {
			c := New(
				&config.Config{APIBaseURL: "https://octo.test"},
				&credential.BotCredential{Token: "bf_t"},
				Options{DryRun: true, ErrOut: io.Discard},
			)
			body, err := c.Do(context.Background(), &Request{
				Method:       http.MethodPost,
				Path:         "/v1/bot/drive/shares",
				Body:         map[string]any{"password": tc.value},
				SecretValues: []string{tc.value},
			})
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			assertTraceMasks(t, string(body), tc.value)
		})
	}
}

// TestSecretValues_NestedAndRepeatedSecretsAreMasked covers the structural walk:
// a secret below the root object, inside an array, and appearing more than once.
func TestSecretValues_NestedAndRepeatedSecretsAreMasked(t *testing.T) {
	const secret = `p@ss"w\ord`

	var trace bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(
		&config.Config{APIBaseURL: srv.URL},
		&credential.BotCredential{Token: "bf_t"},
		Options{Verbose: true, ErrOut: &trace},
	)
	if _, err := c.Do(context.Background(), &Request{
		Method: http.MethodPost,
		Path:   "/v1/bot/probe",
		Body: map[string]any{
			"top":    secret,
			"nested": map[string]any{"inner": secret},
			"list":   []any{"safe", secret},
			"strs":   []string{secret},
			"joined": "prefix-" + secret + "-suffix",
			"id":     json.Number("18446744073709551615"),
		},
		SecretValues: []string{secret},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	assertTraceMasks(t, trace.String(), secret)
	// The structural walk must copy a json.Number through unchanged, or a uint64
	// id would stop being reported at full precision.
	if !strings.Contains(trace.String(), "18446744073709551615") {
		t.Errorf("the masked body lost uint64 precision:\n%s", trace.String())
	}
}

// TestTransportError_DoesNotLeakASecretPathParameter pins the error envelope.
//
// *url.Error.Error() embeds the full request URL, so an x-octo-secret *path*
// parameter — drive.invite.accept's invite_token, drive.share.access /
// share.download's share_token — was formatted straight into the ExitError. That
// is not a --verbose surface: it is the structured error on stderr, emitted
// unconditionally, which for a CLI whose callers capture stderr into logs and
// model context is a wider exposure than the trace the masking was built for.
func TestTransportError_DoesNotLeakASecretPathParameter(t *testing.T) {
	const token = "SUPERSECRETINVITETOKEN123"

	// A closed listener address: the request fails at dial time with a *url.Error.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	c := New(
		&config.Config{APIBaseURL: deadURL},
		&credential.BotCredential{Token: "uk_t"},
		Options{NoRetry: true, ErrOut: io.Discard},
	)
	_, err := c.Do(context.Background(), &Request{
		Method:       http.MethodPost,
		Path:         "/v1/user/drive/invites/" + token + "/accept",
		SecretValues: []string{token},
	})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	if strings.Contains(ee.Message, token) {
		t.Errorf("the error envelope leaked the invite token: %s", ee.Message)
	}
	if !strings.Contains(ee.Message, secretMask) {
		t.Errorf("the error envelope should show the masked value: %s", ee.Message)
	}
	// The rest of the diagnostic must survive — the point is redaction, not
	// swallowing the reason.
	if !strings.Contains(ee.Message, "127.0.0.1") {
		t.Errorf("the error envelope should still name the host: %s", ee.Message)
	}
}

// TestTransportError_RedactionSurvivesRetries checks every attempt's error text,
// not just the last: the retry loop re-formats the transport error each time.
func TestTransportError_RedactionSurvivesRetries(t *testing.T) {
	const token = "SUPERSECRETSHARETOKEN"

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	var trace bytes.Buffer
	c := New(
		&config.Config{APIBaseURL: deadURL},
		&credential.BotCredential{Token: "uk_t"},
		// Verbose so the per-retry "last error" lines are captured too.
		Options{Verbose: true, ErrOut: &trace, Timeout: "2s"},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := c.Do(ctx, &Request{
		Method:       http.MethodPost,
		Path:         "/v1/user/drive/shares/" + token + "/access",
		SecretValues: []string{token},
	})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if ee := output.AsExitError(err); ee != nil && strings.Contains(ee.Message, token) {
		t.Errorf("the final error leaked the share token: %s", ee.Message)
	}
	if strings.Contains(trace.String(), token) {
		t.Errorf("a retry trace line leaked the share token:\n%s", trace.String())
	}
}

// assertTraceMasks fails when secret appears in text in any encoding, and when
// the mask is missing altogether (which would mean the value was dropped rather
// than masked).
func assertTraceMasks(t *testing.T, text, secret string) {
	t.Helper()
	if strings.Contains(text, secret) {
		t.Errorf("output leaked the secret verbatim:\n%s", text)
	}
	for _, form := range secretForms(secret) {
		if form == secret {
			continue
		}
		if strings.Contains(text, form) {
			t.Errorf("output leaked the secret as %q:\n%s", form, text)
		}
	}
	if !strings.Contains(text, secretMask) {
		t.Errorf("output should show the masked value:\n%s", text)
	}
}

// TestBackendError_DoesNotLeakASecretItEchoes pins the response half of the
// error-envelope leak. The transport half (a *url.Error carrying the request URL)
// was closed first; this is the other one — a backend error body that echoes the
// value it was given.
//
// For drive.share.access / drive.share.download / drive.invite.accept the id *is*
// the secret, and a not-found message naming the requested id is the most natural
// thing for a backend to write. ParseBackendError copies the backend's message
// into the envelope and the whole body into Detail, so an unredacted response body
// put the token on stderr unconditionally — no --verbose needed.
func TestBackendError_DoesNotLeakASecretItEchoes(t *testing.T) {
	const token = "SUPERSECRETSHARETOKEN"
	const password = "P@ssw0rd-SECRET"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		// The shape a backend naturally produces: the requested path, which
		// contains the token, echoed back in the message.
		body := `{"code":"not_found","message":"share ` + r.URL.Path +
			` not found (password attempt \"` + password + `\")"}`
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(
		&config.Config{APIBaseURL: srv.URL},
		&credential.BotCredential{Token: "uk_t"},
		Options{NoRetry: true, ErrOut: io.Discard},
	)
	_, err := c.Do(context.Background(), &Request{
		Method:       http.MethodPost,
		Path:         "/v1/user/drive/shares/" + token + "/access",
		Body:         map[string]any{"password": password},
		SecretValues: []string{token, password},
	})
	if err == nil {
		t.Fatal("expected a backend error")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	for _, secret := range []string{token, password} {
		if strings.Contains(ee.Message, secret) {
			t.Errorf("the error message leaked %q: %s", secret, ee.Message)
		}
		if strings.Contains(string(ee.Detail), secret) {
			t.Errorf("the error detail leaked %q: %s", secret, ee.Detail)
		}
	}
	if !strings.Contains(ee.Message, secretMask) && !strings.Contains(string(ee.Detail), secretMask) {
		t.Errorf("expected the masked value to be visible: %s / %s", ee.Message, ee.Detail)
	}
	// The mask contains no quote or backslash, so a JSON Detail stays parseable.
	if len(ee.Detail) > 0 && !json.Valid(ee.Detail) {
		t.Errorf("redaction broke the JSON detail: %s", ee.Detail)
	}
	// The backend's own diagnostic must survive.
	if !strings.Contains(ee.Message, "not_found") && ee.Code != "NOT_FOUND" {
		t.Errorf("the backend's error classification was lost: code=%q message=%q", ee.Code, ee.Message)
	}
}

// TestRedactBodyValue_UnknownShapesFailSafe covers the fallback branch. Every
// secret-bearing body today is a map[string]any, but the engine already builds a
// map[string]string elsewhere, and the day one of those carries a secret the mask
// must not silently no-op.
func TestRedactBodyValue_UnknownShapesFailSafe(t *testing.T) {
	const secret = `p@ss"w\ord`
	secrets := []string{secret}

	cases := []struct {
		name string
		body any
	}{
		{"map[string]string", map[string]string{"password": secret}},
		{"struct", struct {
			Password string `json:"password"`
		}{Password: secret}},
		{"pointer to struct", &struct {
			Password string `json:"password"`
		}{Password: secret}},
		{"slice of maps", []map[string]string{{"password": secret}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := json.Marshal(redactBodyValue(tc.body, secrets))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, form := range secretForms(secret) {
				if strings.Contains(string(buf), form) {
					t.Errorf("the fallback passed the secret through as %q: %s", form, buf)
				}
			}
			if !strings.Contains(string(buf), secretMask) {
				t.Errorf("expected the mask in the rendered body: %s", buf)
			}
		})
	}
}

// TestRedactBodyValue_PreservesScalarsAndNumbers guards the fallback's blast
// radius: it must not reshape values the structural walk already handles, and a
// json.Number must keep its exact digits.
func TestRedactBodyValue_PreservesScalarsAndNumbers(t *testing.T) {
	body := map[string]any{
		"id":    json.Number("18446744073709551615"),
		"count": 7,
		"ratio": 1.5,
		"flag":  true,
		"none":  nil,
		"name":  "public",
	}
	buf, err := json.Marshal(redactBodyValue(body, []string{"unrelated-secret"}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"id":18446744073709551615`, `"count":7`, `"ratio":1.5`, `"flag":true`, `"none":null`, `"name":"public"`} {
		if !strings.Contains(string(buf), want) {
			t.Errorf("rendered body lost %s: %s", want, buf)
		}
	}
}

// TestBackendError_RedactionKeepsTheBodyParseable is the round-4 correction to
// the round-2 fix. redactSecretBytes substituted over the encoded bytes, and the
// substitution is unanchored — so a one-character or punctuation-bearing password
// rewrote the response's own syntax, ParseBackendError could no longer read the
// backend's `code`, and an agent branching on it silently degraded to the generic
// status code and lost `detail`. Nothing leaked; the machine-readable contract
// broke instead.
func TestBackendError_RedactionKeepsTheBodyParseable(t *testing.T) {
	// Every one of these is a substring of the response body below, which is what
	// made the textual substitution destructive.
	for _, password := range []string{"correct-horse-battery", "e", `"`, "s", "o", "a", `\`, "the", " "} {
		t.Run(strconv.Quote(password), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"wrong_password","message":"the share password is incorrect"}`))
			}))
			defer srv.Close()

			c := New(
				&config.Config{APIBaseURL: srv.URL},
				&credential.BotCredential{Token: "uk_t"},
				Options{NoRetry: true, ErrOut: io.Discard},
			)
			_, err := c.Do(context.Background(), &Request{
				Method:       http.MethodPost,
				Path:         "/v1/user/drive/shares/TOKENABC/access",
				Body:         map[string]any{"password": password},
				SecretValues: []string{password},
			})
			if err == nil {
				t.Fatal("expected a backend error")
			}
			ee := output.AsExitError(err)
			if ee == nil {
				t.Fatalf("expected a structured error, got %v", err)
			}
			// The backend's machine-readable code must survive redaction: it is
			// what an agent branches on.
			if ee.Code != "wrong_password" {
				t.Errorf("code: got %q, want wrong_password (redaction must not break parsing)", ee.Code)
			}
			if len(ee.Detail) == 0 {
				t.Error("detail was lost; redaction must not make the body unparseable")
			}
		})
	}
}

// TestBackendError_RedactionSurvivesForeignJSONEscaping covers the other half of
// the same asymmetry. secretForms is built from json.Marshal's spelling, which is
// exhaustive for a Go producer and not for JSON in general — a producer that
// writes `\/` for a slash defeated a substitution over encoded text. Parsing the
// body first and walking the decoded values does not care how it was spelled.
func TestBackendError_RedactionSurvivesForeignJSONEscaping(t *testing.T) {
	const password = "a/b"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		// `\/` is legal JSON and is what several non-Go encoders emit.
		_, _ = w.Write([]byte(`{"error":"wrong_password","message":"bad password a\/b for share"}`))
	}))
	defer srv.Close()

	c := New(
		&config.Config{APIBaseURL: srv.URL},
		&credential.BotCredential{Token: "uk_t"},
		Options{NoRetry: true, ErrOut: io.Discard},
	)
	_, err := c.Do(context.Background(), &Request{
		Method:       http.MethodPost,
		Path:         "/v1/user/drive/shares/TOKENABC/access",
		Body:         map[string]any{"password": password},
		SecretValues: []string{password},
	})
	if err == nil {
		t.Fatal("expected a backend error")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	if strings.Contains(ee.Message, password) || strings.Contains(string(ee.Detail), password) {
		t.Errorf("a foreign-escaped secret survived redaction: %s / %s", ee.Message, ee.Detail)
	}
	if ee.Code != "wrong_password" {
		t.Errorf("code: got %q, want wrong_password", ee.Code)
	}
}

// TestBackendError_NonJSONBodyStillRedacted keeps the fallback covered: a body
// with no structure to walk must still be text-substituted rather than passed
// through.
func TestBackendError_NonJSONBodyStillRedacted(t *testing.T) {
	const token = "SUPERSECRETSHARETOKEN"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream refused share " + token + " (not JSON at all)"))
	}))
	defer srv.Close()

	c := New(
		&config.Config{APIBaseURL: srv.URL},
		&credential.BotCredential{Token: "uk_t"},
		Options{NoRetry: true, ErrOut: io.Discard},
	)
	_, err := c.Do(context.Background(), &Request{
		Method:       http.MethodPost,
		Path:         "/v1/user/drive/shares/" + token + "/access",
		SecretValues: []string{token},
	})
	if err == nil {
		t.Fatal("expected a backend error")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	if strings.Contains(ee.Message, token) || strings.Contains(string(ee.Detail), token) {
		t.Errorf("a non-JSON body leaked the token: %s / %s", ee.Message, ee.Detail)
	}
}

// TestReplaceSecretForm_ShortSecretsUseTokenBoundaries pins the guard that keeps
// short-secret masking from shredding unrelated text, and — just as important —
// that it still masks a genuine echo.
func TestReplaceSecretForm_ShortSecretsUseTokenBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		secret string
		masked bool
	}{
		// Must NOT mask: the letters occur inside unrelated words.
		{"single letter inside a word", `{"error":"wrong_password"}`, "a", false},
		{"single letter inside prose", "the share password is incorrect", "e", false},
		{"short run inside a word", `{"error":"wrong_password"}`, "pass", false},
		// Must mask: a genuine echo of the value.
		{"whole field value", `{"password":"abc123"}`, "abc123", true},
		{"delimited in prose", "bad password abc123 for share", "abc123", true},
		{"whole string", "abc123", "abc123", true},
		{"delimited by punctuation", "password=(a1b2)", "a1b2", true},
		{"single letter as a whole value", `{"password":"e"}`, "e", true},
		// A long secret is masked unconditionally, boundaries or not.
		{"long secret inside a longer run", "xxSUPERSECRETTOKENVALUExx", "SUPERSECRETTOKENVALUE", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := replaceSecretForm(tc.text, tc.secret)
			if masked := strings.Contains(got, secretMask); masked != tc.masked {
				t.Errorf("replaceSecretForm(%q, %q) = %q; masked=%v, want %v",
					tc.text, tc.secret, got, masked, tc.masked)
			}
			if tc.masked && strings.Contains(got, tc.secret) && len([]rune(tc.secret)) >= shortSecretRunes {
				t.Errorf("a long secret survived: %q", got)
			}
		})
	}
}

// TestReplaceSecretForm_ShortSecretIsStillMaskedInEveryEchoShape guards the
// boundary rule against being too clever: every shape a backend or a log line
// realistically echoes a value in must still be masked.
func TestReplaceSecretForm_ShortSecretIsStillMaskedInEveryEchoShape(t *testing.T) {
	const secret = "pw7"
	shapes := []string{
		`{"password":"pw7"}`,
		`password=pw7`,
		`password: pw7`,
		`"pw7"`,
		`bad password 'pw7' rejected`,
		`/v1/user/drive/shares/pw7/access`,
		`pw7`,
		`[pw7]`,
		`pw7 at the start`,
		`at the end pw7`,
	}
	for _, shape := range shapes {
		got := replaceSecretForm(shape, secret)
		if strings.Contains(got, secret) {
			t.Errorf("echo shape %q left the secret in place: %q", shape, got)
		}
	}
}

// Round-8 P1-3. Round 5 made response-side redaction structural so it stopped
// corrupting the body's syntax; round 8 found it still destroys the body's
// *meaning*. redactResponseBody preserves object keys and masks string values —
// and the drive error envelope carries its machine-readable code in a value
// ({"error":"wrong_password"}). A masked code fails looksLikeErrorCode, parsing
// falls back to codeFromStatus, and that branch set no Detail at all.
//
// The consequence is behavioural, not cosmetic: on `share access` the caller
// cannot tell "wrong password, retry" from "no permission, stop".
//
// The trigger set is ordinary, not exotic: any secret of 8+ runes appearing
// anywhere in the code value, and any shorter secret that is a "_"-delimited
// component of it — password, wrong, not, found, denied, expired, invalid.
func TestBackendError_MachineReadableCodeSurvivesRedaction(t *testing.T) {
	cases := []struct {
		name     string
		password string
	}{
		{"long secret unrelated to the code", "correct-horse-battery-staple"},
		{"mixed-case secret", "Passw0rd!"},
		// The two from the round-8 report: 8 runes takes the unconditional
		// substring path, and 5 runes passes the token-boundary rule because "_"
		// is not a word byte.
		{"exactly the short-secret threshold", "password"},
		{"a boundary-delimited component of the code", "wrong"},
		// Every other component of a realistic code vocabulary.
		{"component: not", "not"},
		{"component: found", "found"},
		{"component: denied", "denied"},
		{"component: expired", "expired"},
		{"component: invalid", "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"wrong_password","message":"the share password is incorrect"}`))
			}))
			defer srv.Close()

			c := New(
				&config.Config{APIBaseURL: srv.URL},
				&credential.BotCredential{Token: "uk_t"},
				Options{NoRetry: true, ErrOut: io.Discard},
			)
			_, err := c.Do(context.Background(), &Request{
				Method:       http.MethodPost,
				Path:         "/v1/user/drive/shares/TOKENABC/access",
				Body:         map[string]any{"password": tc.password},
				SecretValues: []string{tc.password},
			})
			if err == nil {
				t.Fatal("expected a backend error")
			}
			ee := output.AsExitError(err)
			if ee == nil {
				t.Fatalf("expected a structured error, got %v", err)
			}
			// Case-sensitive on purpose: the fallback produces FORBIDDEN, and an
			// EqualFold comparison against "wrong_password" would not have noticed
			// that the real code was gone.
			if ee.Code != "wrong_password" {
				t.Errorf("code = %q, want exactly \"wrong_password\"; the caller branches on this to tell "+
					"a retryable wrong password from a terminal permission failure", ee.Code)
			}
			if len(ee.Detail) == 0 {
				t.Error("detail is empty; the fallback branch must carry the backend payload")
			}
			// The secret itself must still be gone from everything human-readable.
			if strings.Contains(ee.Message, tc.password) {
				t.Errorf("the message leaked the password: %s", ee.Message)
			}
		})
	}
}

// TestBackendError_FallbackCarriesDetail pins the other half independently of
// redaction: a body that matches no envelope family at all still has to reach the
// caller as detail, or the only copy of the backend's answer is a truncated
// sentence inside message.
func TestBackendError_FallbackCarriesDetail(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantDetail bool
	}{
		{"unrecognised JSON shape", `{"unexpected":"shape","n":1}`, true},
		{"JSON array", `[{"a":1}]`, true},
		{"free-text error value", `{"error":"missing X-Space-Id"}`, true},
		// Not JSON: embedding it raw would produce an invalid envelope, so detail
		// stays empty and message carries the text.
		{"not JSON at all", `upstream exploded`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ee := output.ParseBackendError(http.StatusBadGateway, []byte(tc.body))
			if ee == nil {
				t.Fatal("expected an ExitError")
			}
			if tc.wantDetail {
				if len(ee.Detail) == 0 {
					t.Errorf("detail is empty for %s; the backend payload was dropped", tc.name)
				} else if !json.Valid(ee.Detail) {
					t.Errorf("detail is not valid JSON, so the envelope would be malformed: %s", ee.Detail)
				}
			} else if len(ee.Detail) != 0 && !json.Valid(ee.Detail) {
				t.Errorf("non-JSON body was spliced into detail as-is: %s", ee.Detail)
			}
		})
	}
}
