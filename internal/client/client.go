// Package client is the REST transport for the Octo gateway. It supports
// module-qualified paths, retry with exponential backoff + jitter, Retry-After,
// verbose logging, and --dry-run.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// Retry defaults per architecture-design.md §6.2.
const (
	defaultMaxRetries  = 3
	defaultBaseDelay   = 500 * time.Millisecond
	defaultMaxDelay    = 10 * time.Second
	defaultTimeout     = 30 * time.Second
	publicAPIMediaType = "application/json"
)

// Options controls client runtime behaviour. Zero values are sensible defaults.
type Options struct {
	Verbose bool
	DryRun  bool
	NoRetry bool
	Timeout string    // raw flag value, parsed once at client construction
	ErrOut  io.Writer // verbose traces and dry-run output go here
}

// Request is a generic API request. Service identifies the logical domain for
// diagnostics; Path is the module-qualified gateway suffix (for example,
// "/fleet/api/v1/tasks/t1"). Body is JSON-encoded if non-nil. Query is merged
// into the URL; headers take precedence over client defaults.
//
// For non-JSON payloads (e.g. multipart uploads) set RawBody + ContentType.
// When RawBody is non-nil, Body is ignored and no JSON marshaling is performed.
type Request struct {
	Service     string
	Method      string
	Path        string
	Query       url.Values
	Body        any
	Headers     map[string]string
	RawBody     []byte
	ContentType string
	// BinaryResponse asks the client to treat 3xx/non-JSON responses as
	// structured metadata envelopes rather than parsing JSON. See file.download.
	BinaryResponse bool
	// OutputPath, when set together with BinaryResponse, makes the client WRITE
	// a 2xx binary body to that file path (instead of only describing it) and
	// return an envelope carrying the saved path + size. Empty preserves the
	// historical describe-only behaviour. A 3xx (redirect-to-URL) response is
	// never written — its URL is surfaced in the envelope as before.
	OutputPath string
	// SuppressSpaceHeader omits the X-Space-Id header even when the active
	// credential carries a SpaceID. It is set for operations whose spec declares
	// x-octo-space-header:false (e.g. the docs bot mount resolves the space
	// server-side). The default (false) preserves the historical behaviour of
	// sending X-Space-Id whenever the credential has a space.
	SuppressSpaceHeader bool
	// SecretValues holds literal values that must never be written to a log:
	// share tokens, invite tokens, and share passwords, declared in the spec via
	// x-octo-secret. Every occurrence is replaced with a mask in --verbose
	// traces and --dry-run output, whether it appears in the URL path or the
	// request body. The values still go on the wire unchanged.
	SecretValues []string
	// SensitiveJSONFields lists top-level writeOnly request properties that
	// must be redacted from dry-run and verbose diagnostic output.
	SensitiveJSONFields []string
}

// secretMask replaces a redacted value in verbose / dry-run output. It is a
// fixed string so the secret's length is not revealed either.
const secretMask = "***REDACTED***"

// redactSecrets masks every non-empty SecretValues entry found in s. Values are
// masked longest-first so a secret that contains another as a substring cannot
// leave a fragment behind.
//
// Every encoding the same value can wear on its way into a log line is masked,
// not just the literal: a secret in a URL path arrives percent-encoded, and one
// inside a marshalled JSON body arrives with `"`, `\` and control characters
// escaped. Masking only the literal is how a password containing a quote used to
// survive a `--verbose` trace verbatim — json.Marshal had already rewritten it,
// so the substitution found nothing to replace.
func redactSecrets(s string, secrets []string) string {
	if s == "" || len(secrets) == 0 {
		return s
	}
	ordered := make([]string, 0, len(secrets))
	for _, v := range secrets {
		if v != "" {
			ordered = append(ordered, v)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, v := range ordered {
		for _, form := range secretForms(v) {
			s = replaceSecretForm(s, form)
		}
	}
	return s
}

// maskOrSuppressValue masks a value, and replaces it wholesale when masking left a
// declared secret behind.
//
// The boundary rule in replaceSecretForm is right for prose and is deliberately kept: a
// four-character password is a substring of half the English language, and masking every
// occurrence of it shreds text that has nothing to do with the secret. But one constant
// was being asked two different questions — "is this value distinctive enough to search
// for globally" and "may this value be disclosed" — and the second one has no threshold.
// A seven-character password echoed with one adjacent alphanumeric character
// (`"bad password pw12345x"`) is not at a token boundary, so masking declined, and the
// value went out in full.
//
// Splitting the two questions: masking keeps its boundary semantics, and if a secret is
// still literally present afterwards the whole value is suppressed instead of published.
// Suppressing the field costs a line of diagnostic text; publishing it costs the user
// their password.
func maskOrSuppressValue(v string, secrets []string) string {
	masked := redactSecrets(v, secrets)
	if containsAnySecret(masked, secrets) {
		return secretMask
	}
	return masked
}

// maskScalarValue applies maskOrSuppressValue's disclosure rule to a non-string JSON scalar,
// keeping the value's own type when nothing is disclosed.
//
// The question is asked of the value's JSON spelling, which is what a reader of the envelope
// actually sees. That keeps the two failure modes apart. An unrelated numeric id is
// untouched, because its digits do not contain a declared secret — so the rule does not mask
// ordinary ids out of every diagnostic, which is the damage over-declaring causes. And a
// declared secret echoed as a number is suppressed, because its digits *are* the secret.
//
// Suppression replaces the number with the mask string, changing the JSON type of that one
// field. That is the intended trade: this runs only on an error body being printed for a
// human or an agent, the alternative is publishing the value, and a caller branching on a
// machine-readable code reads it from the code position, which redactErrorEnvelope exempts
// by its own rule rather than through this one.
func maskScalarValue(v any, secrets []string) any {
	buf, err := json.Marshal(v)
	if err != nil {
		return secretMask
	}
	text := string(buf)
	if redactSecrets(text, secrets) == text && !containsAnySecret(text, secrets) {
		return v
	}
	return secretMask
}

// containsAnySecret reports whether any declared secret is still literally present in s.
// Deliberately a raw substring test with no boundary or length condition: the question
// here is disclosure, not whether a match is meaningful enough to act on.
func containsAnySecret(s string, secrets []string) bool {
	if s == "" {
		return false
	}
	for _, v := range secrets {
		if v == "" {
			continue
		}
		for _, form := range secretForms(v) {
			if form != "" && strings.Contains(s, form) {
				return true
			}
		}
	}
	return false
}

// shortSecretRunes is the length below which a secret is masked only where it
// appears as a whole token rather than anywhere at all.
//
// Substring masking is right for anything long enough that a match means
// something. For a very short value it inverts: a one-character password is a
// substring of almost every message, so masking every occurrence rewrites text
// that has nothing to do with the secret — including a backend's own error code —
// while protecting a value that carries almost no secrecy to begin with. Eight is
// chosen so a token-length value keeps unconditional masking and a
// keyboard-mashed password does not shred the response it appears in.
const shortSecretRunes = 8

// replaceSecretForm masks one encoded spelling of a secret in s.
//
// A long form is replaced everywhere. A short one is replaced only where it is
// delimited by non-alphanumeric characters (or the ends of the string), so a
// genuine echo — `"password":"abc123"`, or `bad password abc123 for share` — is
// still caught while the letters of an unrelated word are not.
func replaceSecretForm(s, form string) string {
	if form == "" {
		return s
	}
	if len([]rune(form)) >= shortSecretRunes {
		return strings.ReplaceAll(s, form, secretMask)
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], form) && isTokenBoundary(s, i, i+len(form)) {
			b.WriteString(secretMask)
			i += len(form)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// isTokenBoundary reports whether s[start:end] is delimited by something other
// than an alphanumeric character on both sides, which is what distinguishes a
// value echoed back from the same letters occurring inside an unrelated word.
func isTokenBoundary(s string, start, end int) bool {
	return !isWordByte(s, start-1) && !isWordByte(s, end)
}

func isWordByte(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// secretForms lists the encodings of v that could appear in text destined for a
// log: the literal, the two percent-encoded forms, and the JSON-string body
// (without its surrounding quotes).
func secretForms(v string) []string {
	forms := []string{v}
	for _, enc := range []string{url.PathEscape(v), url.QueryEscape(v), jsonStringBody(v), asciiEscapedBody(v)} {
		if enc != v && enc != "" {
			forms = append(forms, enc)
		}
	}
	return forms
}

// asciiEscapedBody returns v with every non-ASCII rune written as \uXXXX, which is what
// a JSON encoder in ASCII-only mode produces — Python's json.dumps does this by default.
//
// Go's marshaller does not: it emits non-ASCII as UTF-8 and escapes only <, > and &, so
// jsonStringBody never covers this spelling. On the JSON response path the difference is
// invisible, because the body is parsed and the escape is decoded before the walk sees the
// value. It matters on the non-JSON fallback, which substitutes over undecoded bytes — and
// non-JSON error bodies are exactly the nginx and WAF pages that can echo a blocked
// request's content.
func asciiEscapedBody(v string) string {
	var b strings.Builder
	var nonASCII bool
	for _, r := range v {
		if r < utf8.RuneSelf {
			b.WriteRune(r)
			continue
		}
		nonASCII = true
		fmt.Fprintf(&b, "\\u%04x", r)
	}
	if !nonASCII {
		return "" // no different from the literal; the caller skips duplicates
	}
	return b.String()
}

// jsonStringBody returns v as json.Marshal would render it inside a JSON
// document, minus the surrounding quotes. Returns "" when v needs no escaping,
// so the caller skips a duplicate of the literal form.
func jsonStringBody(v string) string {
	buf, err := json.Marshal(v)
	if err != nil || len(buf) < 2 {
		return ""
	}
	return string(buf[1 : len(buf)-1])
}

// redactBodyForLog renders req's JSON body for a verbose trace or a dry-run
// description with every declared secret masked.
//
// Where the body is a Go value the client marshalled itself, the masking is
// structural: the value is walked and matching leaf strings are replaced *before*
// marshalling, so the mask lands on the value the caller actually passed rather
// than on whatever escaped form it took in the output. A RawBody (multipart) has
// no structure to walk, so it falls back to text substitution over the encoded
// forms. Either way the bytes on the wire are untouched — only the log copy is.
func redactBodyForLog(req *Request, marshalled []byte) string {
	if len(req.SecretValues) == 0 {
		return string(marshalled)
	}
	if req.Body != nil && len(req.RawBody) == 0 {
		// Keys are redacted on the request side and not on the response side. A
		// request body's keys are caller-controlled — --data merges arbitrary JSON,
		// so a secret can land in a key — while a response body's keys are the
		// backend's contract and masking them is what broke error parsing.
		if buf, err := json.Marshal(redactBodyKeysAndValues(req.Body, req.SecretValues)); err == nil {
			return string(buf)
		}
	}
	return redactSecrets(string(marshalled), req.SecretValues)
}

// redactResponseBody masks secrets in a backend error body before it is parsed.
//
// The masking is structural for the same reason the request side is: a textual
// substitution over the encoded bytes is both too broad and too narrow. Too
// broad, because it is unanchored — a one-character or punctuation password
// rewrites the body's own syntax, and ParseBackendError then cannot read the
// backend's `code`, so an agent branching on it silently degrades to the generic
// status code and loses `detail`. Too narrow, because the encoded spelling is
// only guessable for a Go producer: a backend that writes `\/` for a slash
// defeats a substitution built from json.Marshal's spelling.
//
// Parsing first and walking the decoded values fixes both. A body that is not JSON
// has no structure to walk and falls back to text substitution.
//
// # Two rules the fallback needs and the structural walk does not
//
// Only a *complete* JSON body takes the structural path. json.Decoder.Decode reads one
// value and does not require EOF, so a body merely starting with a JSON token was rewritten
// to just that token and the rest discarded — `404 page not found`, which is what Go's own
// http.NotFound writes, became the number 404. That is the same trap decodeStrict was added
// to close on the input side, and json.Valid is the one-call form of the same question.
// The direction is over-redaction rather than disclosure, so nothing leaked; what it
// destroyed was the message/detail contract this CLI exists to hand to an agent, in exactly
// the case an operator most needs the text.
//
// And the text fallback fails closed, because bare masking is not enough there. The
// boundary rule in replaceSecretForm declines a secret shorter than shortSecretRunes unless
// it sits at a token boundary, so a single adjacent alphanumeric character defeats it —
// and `password` is x-octo-secret with no minLength, so a 1-7 character password is an
// accepted input rather than a hypothetical. ParseBackendError copies a non-JSON body
// straight into Message, which the envelope prints with no --verbose needed, and a WAF or
// reverse proxy answering text/html is the ordinary way that body arrives. So the fallback
// asks the disclosure question the string leaves already ask — maskOrSuppressValue's
// question — and suppresses the whole body when the answer is yes. A body echoing no secret
// is untouched, which is what keeps a secret-free diagnostic readable.
//
// Object keys are masked too, on the same rule as the values: mask when masking would
// remove a declared secret, leave alone otherwise. An earlier version copied keys
// verbatim, justified as "keys carry the response contract, never a secret" and as what
// "keeps the body parseable". The parseability half was inherited from the textual
// substitution this replaced and does not apply to a structural walk — renaming a key
// in a decoded map and re-marshalling yields valid JSON by construction, which is why
// redactBodyKeysAndValues has always masked keys on the request side. With that gone
// the contract half does not carry the claim either: a contract field name is fixed by
// the API and cannot become a caller-supplied token, so nothing a caller branches on is
// touched — while a backend that keys a map by a caller-supplied id, the ordinary shape
// of a per-id batch result, used to put a share token straight into the printed envelope.
//
// Two consequences worth knowing rather than discovering. The keys this CLI itself reads
// out of a body — see isEnvelopeContractKey — are never masked, because masking them
// destroys what a caller branches on while hiding a name the API fixes anyway; that is
// clause 1's argument applied to the key. And if two other keys in the same object both
// carry secrets and mask to the same string, they collapse to one entry: both names were
// being destroyed on purpose, and the walk iterates in sorted order so the same input
// always collapses the same way.
func redactResponseBody(b []byte, secrets []string) []byte {
	if len(b) == 0 || len(secrets) == 0 {
		return b
	}
	if json.Valid(b) {
		var parsed any
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&parsed); err == nil {
			if buf, merr := json.Marshal(redactErrorEnvelope(parsed, secrets)); merr == nil {
				return buf
			}
		}
	}
	out := redactSecrets(string(b), secrets)
	if containsAnySecret(out, secrets) {
		// The whole body, not the occurrence: an opaque body has no structure to bound the
		// suppression to, and suppressing a diagnostic costs a line of text while
		// publishing it costs the user their password.
		return []byte(secretMask)
	}
	return []byte(out)
}

// redactErrorEnvelope masks a decoded error body, leaving the machine-readable
// code alone.
//
// The code is what a caller branches on — "wrong password, retry" versus "no
// permission, stop" — and it lives in a *value*, not a key: the drive envelope is
// {"error":"wrong_password"}, the matters envelope {"error":{"code":…}}. Masking it
// made the value fail looksLikeErrorCode, so parsing fell through to a generic
// status-derived code and the caller lost the distinction.
//
// # The exemption rule
//
// Two goals collide here, and the resolution is stated rather than assumed:
// exempting the code position protects the error contract, while bounding the
// exemption protects the secret. They genuinely conflict in one case — a share id
// that happens to be spelled `not_found`, where the secret *is* a valid code — so
// the rule decides by asking whether the value is a code this CLI **recognises**,
// which is a property no caller-supplied id can acquire:
//
//  1. A recognised code (present in the backend error mapping) is never masked,
//     even when it equals a declared secret. The vocabulary is closed and the CLI
//     prints these codes on every failure of their kind, so leaving one discloses
//     nothing an attacker could not already predict — while masking it destroys the
//     distinction the caller branches on.
//  2. An unrecognised value in the code position from which masking would remove a
//     declared secret IS masked. Nothing vouches for it, so the secret wins. This is
//     the case that shape alone used to let through: `lowercasesecretid` looks
//     exactly like a code and is not one.
//  3. An unrecognised value that carries no secret is left alone, so a code the
//     backend adds tomorrow still reaches the caller instead of being masked for
//     being unfamiliar.
//
// Clause 2 asks "would masking take something out of this value", not "is this value
// the secret". Equality was the wrong bound and had a trivial bypass: code shape is
// satisfied by wrapping the id in more code shape, so `not_found_<id>`,
// `<id>_not_found` and `share_<id>_error` all sailed through clause 3 carrying the
// id whole. It was also inconsistent — the identical string under a key the
// exemption does not recognise (errors[].code) was masked, so one envelope redacted
// a value that another printed. Deferring to the masker makes the code position
// agree with every other position by construction, which is the only version of this
// that stays true as the masker grows token-boundary rules.
//
// What clause 2 does and does not reach, so the next reader does not have to derive
// it: the question is delegated to the masker, so the exemption is exactly as strong
// as masking is. Whatever the masker recognises — a whole value, an embedded
// substring, a short secret at a token boundary — is refused the exemption. What the
// masker cannot see, this cannot either: a secret the caller never declared in
// SecretValues, and a value that carries the secret in some *transformed* form
// (re-encoded, hashed, split across two fields) is not recognised by either, here or
// anywhere else in the redaction path. That bound is a property of the masker, not of
// this rule, which is the point of delegating rather than re-deciding.
//
// Why a real token never reaches clause 1 or 3 by accident: Octo share and invite
// tokens are base64url, so they carry upper-case characters or "-", and
// IsErrorCodeShaped admits only lower-case alphanumerics and underscores. A token
// therefore fails the shape gate before the vocabulary question is asked.
func redactErrorEnvelope(v any, secrets []string) any {
	obj, ok := v.(map[string]any)
	if !ok {
		return redactBodyValue(v, secrets)
	}
	out := make(map[string]any, len(obj))
	// Sorted, not map order: two keys that both carry secrets mask to the same string
	// and collapse to one entry, and with Go's randomised range order *which* value
	// survived changed between runs. Collapsing is accepted — both names were being
	// destroyed on purpose — but producing different output for the same input is not,
	// for a CLI whose callers parse this. First key in sorted order wins.
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		val := obj[key]
		// The semantic decisions below are made on the key as the backend wrote it —
		// whether it is the code-bearing field, whether it is the nested envelope —
		// while what gets written is the masked spelling.
		outKey := maskKey(key, secrets)
		if _, taken := out[outKey]; taken {
			continue // an earlier key already masked to this name
		}
		switch {
		case isCodeBearingKey(key) && isExemptCode(val, secrets):
			out[outKey] = val
		case key == "error":
			// The matters envelope nests {"code":…,"message":…} under "error";
			// recurse so its code is exempt too while its message is masked.
			out[outKey] = redactErrorEnvelope(val, secrets)
		default:
			out[outKey] = redactBodyValue(val, secrets)
		}
	}
	return out
}

// maskKey masks an object key unless the key names the envelope contract.
//
// The exemption is clause 1's argument applied to the key instead of the value. A
// contract field name is fixed by the API, so leaving it discloses nothing a caller
// could not already predict, while masking it destroys what they branch on — and an
// exact-match secret *is* replaced regardless of length, so a caller whose password
// happens to be an ordinary word like "code" or "message" had the envelope's own field
// names rewritten. The earlier reasoning here — that short secrets are only substituted
// at a token boundary — covers a secret sitting *inside* "code" and not a secret that is
// "code".
//
// This keeps the key rule and the value rule independent: the key decides which value
// rule applies, and a contract key is never rewritten, so the value's exemption
// decision cannot be changed by masking.
//
// A non-contract key gets the value rule in full, suppression included. Masking alone would
// leave the key position with the same short-secret hole the string leaves had: the boundary
// rule declines a secret under shortSecretRunes unless it is delimited, so a backend keying
// a map by "<password>x" would have published it.
func maskKey(key string, secrets []string) string {
	if isEnvelopeContractKey(key) {
		return key
	}
	return maskOrSuppressValue(key, secrets)
}

// isEnvelopeContractKey names the keys this CLI itself reads out of a backend body.
// Keeping them legible is the whole point of the redaction being structural.
func isEnvelopeContractKey(key string) bool {
	switch key {
	case "code", "error", "errors", "message", "msg", "detail", "details", "data":
		return true
	}
	return false
}

// isCodeBearingKey names the keys the three envelope families put a
// machine-readable code under.
func isCodeBearingKey(key string) bool {
	return key == "code" || key == "error"
}

// isExemptCode applies the shape gate and three-clause rule documented on
// redactErrorEnvelope.
func isExemptCode(val any, secrets []string) bool {
	s, ok := val.(string)
	if !ok || !output.IsErrorCodeShaped(s) {
		return false
	}
	if output.IsKnownErrorCode(s) {
		return true // clause 1
	}
	// Clause 2: not exempt if the value carries a declared secret at all. Two tests,
	// because they catch different things: containsAnySecret is a raw substring check,
	// which is what a short secret needs since the masker's boundary rule would decline
	// it; and "masking would change this" additionally catches an *encoded* spelling
	// that is not present literally. Asking only the second question — which is what
	// this used to do — let a short secret through, because the masker answering "no
	// change" meant "not at a token boundary", not "no secret here".
	if containsAnySecret(s, secrets) || redactSecrets(s, secrets) != s {
		return false
	}
	return true // clause 3
}

// redactBodyKeysAndValues is redactBodyValue plus key masking, for a body whose
// keys the caller supplied.
//
// Keys and values get the identical rule — maskOrSuppressValue, suppression included. Bare
// redactSecrets here left the request side with the short-secret hole the response side had:
// the boundary rule declines a secret under shortSecretRunes unless it is delimited, so a
// caller who merged `--data '{"pw12345x":...}'` had the key printed in the --verbose trace and
// the --dry-run description. There is no contract-key exemption on this side, because these
// names are the caller's rather than the API's — which is also why keys are masked here at
// all.
//
// Colliding masked keys are collapsed in sorted order, matching redactBodyValue. Ranging a Go
// map is randomised, so with two keys that mask to the same string the surviving value was
// whichever one the runtime visited last — a dry-run description that differed between
// identical invocations.
func redactBodyKeysAndValues(v any, secrets []string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for _, key := range sortedKeys(t) {
			outKey := maskOrSuppressValue(key, secrets)
			if _, taken := out[outKey]; taken {
				continue
			}
			out[outKey] = redactBodyKeysAndValues(t[key], secrets)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(t))
		for _, key := range sortedStringKeys(t) {
			outKey := maskOrSuppressValue(key, secrets)
			if _, taken := out[outKey]; taken {
				continue
			}
			out[outKey] = maskOrSuppressValue(t[key], secrets)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactBodyKeysAndValues(val, secrets)
		}
		return out
	}
	return redactBodyValue(v, secrets)
}

// sortedKeys returns m's keys in a fixed order, so a masked collision always collapses the
// same way.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedStringKeys is sortedKeys for a map[string]string body.
func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// redactBodyValue deep-copies v with every leaf run through the masker.
//
// A declared secret is masked whatever JSON kind it arrives as. The earlier version masked
// only `case string`, on the reading that a secret is always a string — but a backend
// echoing a declared value as a JSON number defeated that with no mistake on the caller's
// part, and an all-digit share password or an id echoed as a number is the ordinary shape.
// Non-string scalars therefore go through maskScalarValue, which decides on the value's own
// JSON spelling, so an unrelated id still comes through untouched.
//
// The default branch marshals and text-redacts rather than passing the value
// through: a body shape this function does not walk structurally (a
// map[string]string, say — cmd/service/aliases.go already builds one) would
// otherwise have its secrets silently unmasked, with no test failing. Failing
// safe by construction is worth the marshal on a path that only runs for
// secret-bearing requests.
func redactBodyValue(v any, secrets []string) any {
	switch t := v.(type) {
	case string:
		return maskOrSuppressValue(t, secrets)
	case nil:
		// JSON null is the one scalar whose spelling is fixed by the grammar rather than
		// derived from a value, so there is nothing here to disclose.
		return v
	case json.Number, bool, int, int64, float64:
		return maskScalarValue(v, secrets)
	case map[string]any:
		out := make(map[string]any, len(t))
		// Sorted for the same reason as the envelope walk: colliding masked keys must
		// collapse the same way on every run.
		for _, key := range sortedKeys(t) {
			// Keys are masked on the same rule as values, minus the contract names.
			// On a request body built from the spec this is a no-op, because a
			// spec-derived key cannot contain a declared secret; on a response body it
			// is what stops a backend that keys a map by a caller-supplied id from
			// printing it.
			outKey := maskKey(key, secrets)
			if _, taken := out[outKey]; taken {
				continue
			}
			out[outKey] = redactBodyValue(t[key], secrets)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactBodyValue(val, secrets)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, val := range t {
			out[i] = redactSecrets(val, secrets)
		}
		return out
	}
	// Unknown shape: render it, mask the text, and hand back the masked JSON so
	// the value is never emitted unredacted.
	buf, err := json.Marshal(v)
	if err != nil {
		return secretMask
	}
	return json.RawMessage(redactSecrets(string(buf), secrets))
}

// Client is the REST client. Created via New; invoked by command layer via Do.
//
// Tests should control retry timing by setting Options.NoRetry=true (to
// bypass the retry loop entirely) or by keeping the test context bounded so
// the select in doWithRetry exits via ctx.Done(). There is no test-only
// clock hook on Client; the retry scheduling is intentionally minimal.
type Client struct {
	cfg        *config.Config
	cred       *credential.BotCredential
	httpClient *http.Client
	options    Options
}

// New constructs a Client. Timeout is parsed here; invalid values fall back to
// the default and emit a verbose warning (not a hard error — a bad flag value
// shouldn't fail commands that wouldn't otherwise need HTTP).
func New(cfg *config.Config, cred *credential.BotCredential, opts Options) *Client {
	timeout := defaultTimeout
	if opts.Timeout != "" {
		if d, err := time.ParseDuration(opts.Timeout); err == nil {
			timeout = d
		} else if opts.ErrOut != nil {
			fmt.Fprintf(opts.ErrOut, "warning: invalid --timeout %q; using %s\n", opts.Timeout, defaultTimeout) //nolint:errcheck // stderr warning
		}
	}
	return &Client{
		cfg:  cfg,
		cred: cred,
		httpClient: &http.Client{
			Timeout: timeout,
			// Don't auto-follow — file.download returns 302 to a presigned URL
			// and we want to surface that URL in the envelope, not fetch it.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		options: opts,
	}
}

// Do performs req against the service URL, applying auth, retry, and dry-run.
// Returns the raw response body on 2xx; an *output.ExitError on non-2xx or
// transport failure.
//
// Every error leaving this method is redacted against req.SecretValues. That is
// deliberately a boundary rather than a set of per-site calls: three rounds of
// review each found another site that formatted a secret-bearing string into an
// error, because masking had been installed around the transport window and
// anything raised outside it — URL construction, service routing, body marshal,
// response read — echoed freely. Redacting on the way out makes the property hold
// for sites that do not exist yet.
func (c *Client) Do(ctx context.Context, req *Request) ([]byte, error) {
	body, err := c.do(ctx, req)
	if err != nil {
		return nil, redactError(err, req.SecretValues)
	}
	return body, nil
}

// redactError returns err with every declared secret masked in its human-readable
// fields. The concrete type is preserved where it carries meaning: a
// *retryableErr is rebuilt as one so a caller inspecting retryability still sees
// it, and a plain error with nothing to redact is returned untouched.
func redactError(err error, secrets []string) error {
	if err == nil || len(secrets) == 0 {
		return err
	}
	var re *retryableErr
	if errors.As(err, &re) && re.ExitError != nil {
		return &retryableErr{
			ExitError:  redactExitError(re.ExitError, secrets),
			retryAfter: re.retryAfter,
		}
	}
	var ee *output.ExitError
	if errors.As(err, &ee) {
		return redactExitError(ee, secrets)
	}
	// Not one of ours: wrap so the text is still masked rather than passed
	// through, since the caller renders whatever it receives.
	return output.ErrNetwork(redactSecrets(err.Error(), secrets), "")
}

// redactExitError copies e with its message, hint and detail masked. A copy
// rather than a mutation: the value may be shared (a package-level sentinel, or
// an error already handed to another caller).
func redactExitError(e *output.ExitError, secrets []string) *output.ExitError {
	if e == nil {
		return nil
	}
	out := *e
	out.Message = redactSecrets(e.Message, secrets)
	out.Hint = redactSecrets(e.Hint, secrets)
	if len(e.Detail) > 0 {
		out.Detail = redactSecretBytes(e.Detail, secrets)
	}
	return &out
}

// redactSecretBytes masks secrets in a JSON fragment, preserving its structure
// where it parses so a detail payload stays readable.
func redactSecretBytes(b []byte, secrets []string) []byte {
	if len(b) == 0 || len(secrets) == 0 {
		return b
	}
	return redactResponseBody(b, secrets)
}

// do is Do's body. It returns errors unredacted; Do is the single place that
// masks them.
func (c *Client) do(ctx context.Context, req *Request) ([]byte, error) {
	if req.Service == "" {
		req.Service = "default"
	}

	// Route the message-search family by chat-id scope and token kind
	// (no channel_id → cross-session global; uk_ → /v1/user; app_ → error).
	// Non-search paths pass through unchanged.
	routedPath, err := routeSearchPath(c.cred, req.Path, req.Body)
	if err != nil {
		return nil, err
	}
	req.Path = routedPath

	base := c.cfg.ServiceURL(req.Service)
	if base == "" {
		return nil, output.ErrValidation(
			fmt.Sprintf("no base URL configured for service %q", req.Service),
			fmt.Sprintf("set %s", config.EnvAPIBaseURL),
		)
	}

	u, err := buildURL(base, req.Path, req.Query)
	if err != nil {
		return nil, output.ErrValidation(err.Error(), "check path and query parameters")
	}

	var bodyBytes []byte
	contentType := ""
	if len(req.RawBody) > 0 {
		bodyBytes = req.RawBody
		contentType = req.ContentType
	} else if req.Body != nil {
		bodyBytes, err = json.Marshal(req.Body)
		if err != nil {
			return nil, output.ErrWithHint("internal", "MARSHAL_FAILED", fmt.Sprintf("marshal request body: %v", err), "")
		}
		contentType = "application/json"
	}

	if req.Service == "loop" && headerValue(req.Headers, "Accept") == "" {
		if req.Headers == nil {
			req.Headers = make(map[string]string)
		}
		req.Headers["Accept"] = publicAPIMediaType
	}

	if c.options.DryRun {
		return c.renderDryRun(req, u, bodyBytes)
	}

	return c.doWithRetry(ctx, req, u, bodyBytes, contentType)
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// doWithRetry runs the HTTP request, retrying transient errors with backoff.
func (c *Client) doWithRetry(ctx context.Context, req *Request, urlStr string, body []byte, contentType string) ([]byte, error) {
	maxRetries := defaultMaxRetries
	if c.options.NoRetry {
		maxRetries = 0
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt)
			// Respect Retry-After if the last response supplied one. It is
			// attached to the *ExitError via Detail? No — we pass it in via
			// lastRetryAfter below.
			if d, ok := extractRetryAfterFromErr(lastErr); ok {
				delay = d
			}
			// Redacted here, not relied on later: the masking boundary is on Do's
			// *return* value, and lastErr inside this loop has not passed through it
			// yet. A transport failure is a *url.Error whose text quotes the whole
			// URL, so on a share or invite path that is the token — printed once per
			// retry.
			c.verbosef("retry #%d after %s (last error: %v)", attempt, delay,
				redactError(lastErr, req.SecretValues))
			select {
			case <-ctx.Done():
				return nil, output.ErrNetwork(ctx.Err().Error(), "request cancelled")
			case <-time.After(delay):
			}
		}

		body, err := c.attempt(ctx, req, urlStr, body, contentType)
		if err == nil {
			return body, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// attempt executes one HTTP round-trip and interprets the response.
func (c *Client) attempt(ctx context.Context, req *Request, urlStr string, body []byte, contentType string) ([]byte, error) { //nolint:gocyclo // HTTP attempt handles auth, headers, dry-run, binary, retries in one flow
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, urlStr, reader)
	if err != nil {
		return nil, output.ErrNetwork(redactSecrets(err.Error(), req.SecretValues), "invalid request")
	}

	if c.cred != nil && c.cred.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cred.Token)
	}
	if c.cred != nil && c.cred.SpaceID != "" && !req.SuppressSpaceHeader {
		httpReq.Header.Set("X-Space-Id", c.cred.SpaceID)
	}
	if body != nil && contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	c.verbosef("%s %s", req.Method, redactSecrets(urlStr, req.SecretValues))
	if c.options.Verbose && len(body) > 0 {
		c.verbosef("request body: %s", truncate(redactDiagnosticBody(req, body), 1024))
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// *url.Error.Error() embeds the whole request URL, so an x-octo-secret
		// path parameter (invite / share token) would otherwise reach the default
		// stderr error envelope — no --verbose needed to leak it.
		msg := redactSecrets(err.Error(), req.SecretValues)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, output.ErrNetwork(msg, "request timed out or was cancelled")
		}
		return nil, &retryableErr{
			ExitError: output.ErrNetwork(msg, "transport error; will retry"),
		}
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on HTTP response body

	// The whole response body is buffered here. Every downstream branch needs
	// it fully in memory anyway — JSON parsing, backend-error parsing, and the
	// binary describe envelope (which reports size = len(body)) all consume the
	// complete payload. Board PNG/SVG exports are bounded and small, so a size
	// cap / streaming-to-temp path would add complexity without a real payoff
	// today; deferred until an operation returns genuinely large bodies.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, output.ErrNetwork(fmt.Sprintf("read response: %v", err), "")
	}

	c.verbosef("← %d (%d bytes)", resp.StatusCode, len(respBody))

	// Redirects (3xx) are not followed automatically — surface the Location
	// header as a JSON envelope when the caller opted into binary/redirect
	// handling (file.download). Any other endpoint returning 3xx is treated
	// as an unexpected error.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if req.BinaryResponse {
			// Redacted like the sibling 3xx error path below. No secret-bearing
			// operation declares a binary response today, so this does not fire —
			// but the two renderings of the same header disagreeing is the kind of
			// asymmetry that becomes a finding a round later.
			loc := redactSecrets(resp.Header.Get("Location"), req.SecretValues)
			env := map[string]any{
				"url":    loc,
				"status": resp.StatusCode,
			}
			if ct := resp.Header.Get("Content-Type"); ct != "" {
				env["content_type"] = ct
			}
			return json.Marshal(env)
		}
		return nil, output.ErrAPI(
			fmt.Sprintf("HTTP_%d", resp.StatusCode),
			fmt.Sprintf("unexpected redirect to %q", redactSecrets(resp.Header.Get("Location"), req.SecretValues)),
			"",
		)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Binary/redirect opt-in: don't try to parse as JSON, just describe.
		if req.BinaryResponse {
			env := map[string]any{
				"status":       resp.StatusCode,
				"content_type": resp.Header.Get("Content-Type"),
				"size":         len(respBody),
			}
			// When the caller asked for a destination, WRITE the bytes to disk
			// (docs.scene.export --output). A write failure is a hard, non-
			// retryable error — the request succeeded, so retrying would only
			// re-download; the problem is local (bad path/permissions).
			//
			// The write is atomic: bytes go to a temp file in the destination
			// directory and are renamed into place only after a fully successful
			// write. A mid-write failure (disk full, I/O error, cancellation)
			// therefore never leaves a truncated/empty file, and never clobbers
			// an existing good copy at outputPath — the rename is all-or-nothing.
			if req.OutputPath != "" {
				if err := writeFileAtomic(req.OutputPath, respBody, 0o644); err != nil {
					return nil, output.ErrValidation(
						fmt.Sprintf("write output %q: %v", req.OutputPath, err),
						"check the path is writable and the directory exists",
					)
				}
				env["output"] = req.OutputPath
			}
			return json.Marshal(env)
		}
		return respBody, nil
	}

	// A backend error body can echo the value it was given, and a not-found
	// message naming the requested id is the most natural thing for a backend to
	// write — which for drive.share.access / drive.share.download /
	// drive.invite.accept means the id *is* the secret. ParseBackendError copies
	// the backend's message into the envelope and the whole body into Detail, so
	// the response path is redacted here as the transport path already is. This is
	// not a --verbose surface: it is the structured error on stderr, emitted
	// unconditionally. The mask contains no quote or backslash, so Detail stays
	// valid JSON.
	ee := output.ParseBackendError(resp.StatusCode, redactResponseBody(respBody, req.SecretValues))

	if isRetryableStatus(resp.StatusCode) {
		re := &retryableErr{ExitError: ee}
		if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
			re.retryAfter = ra
		}
		return nil, re
	}
	return nil, ee
}

// writeFileAtomic writes data to path atomically: it streams the bytes into a
// temp file in the same directory, then renames it over path. Because rename
// within a directory is atomic, path is only ever the old file (untouched) or
// the fully-written new file — never a partial/truncated result, and an
// existing good copy is never clobbered when the write fails midway. On any
// error the temp file is removed so no stray temp files accumulate.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Remove the temp file unless the rename below hands it off successfully.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName) //nolint:errcheck // best-effort cleanup
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() //nolint:errcheck // already returning the write error
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close() //nolint:errcheck // already returning the chmod error
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

// renderDryRun writes a human-readable request description and returns a
// synthetic success body containing the same payload for envelope rendering.
// Spec-declared secrets (share/invite tokens, share passwords) are masked in
// both the URL and the body — a dry run must be safe to paste into a ticket.
func (c *Client) renderDryRun(req *Request, urlStr string, body []byte) ([]byte, error) {
	var bodyField any
	if len(body) > 0 {
		redacted := redactDiagnosticBody(req, body)
		// UseNumber so a uint64 id in the echoed body is shown at full precision;
		// a plain unmarshal would round it and make --dry-run misreport what the
		// request actually carries.
		dec := json.NewDecoder(strings.NewReader(redacted))
		dec.UseNumber()
		if err := dec.Decode(&bodyField); err != nil {
			bodyField = redacted
		}
	}
	hdr := map[string]string{}
	for k, v := range req.Headers {
		hdr[k] = redactSecrets(v, req.SecretValues)
	}
	removeHeader(hdr, "Authorization")
	if c.cred != nil && c.cred.Token != "" {
		hdr["Authorization"] = "Bearer " + credential.MaskToken(c.cred.Token)
	}
	if c.cred != nil && c.cred.SpaceID != "" && !req.SuppressSpaceHeader {
		hdr["X-Space-Id"] = c.cred.SpaceID
	}
	out := map[string]any{
		"dry_run": true,
		"method":  req.Method,
		"url":     redactSecrets(urlStr, req.SecretValues),
		"headers": hdr,
	}
	if bodyField != nil {
		out["body"] = bodyField
	}
	buf, err := json.Marshal(out)
	if err != nil {
		return nil, output.ErrWithHint("internal", "MARSHAL_FAILED", err.Error(), "")
	}
	return buf, nil
}

func removeHeader(headers map[string]string, name string) {
	for key := range headers {
		if strings.EqualFold(key, name) {
			delete(headers, key)
		}
	}
}

func redactJSONBody(body []byte, sensitiveFields []string) []byte {
	if len(body) == 0 || len(sensitiveFields) == 0 {
		return body
	}
	var value map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return body
	}
	for _, field := range sensitiveFields {
		if _, ok := value[field]; ok {
			value[field] = "[REDACTED]"
		}
	}
	redacted, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return redacted
}

func redactDiagnosticBody(req *Request, body []byte) string {
	redacted := []byte(redactBodyForLog(req, body))
	return string(redactJSONBody(redacted, req.SensitiveJSONFields))
}

// --- helpers ---

func (c *Client) verbosef(format string, args ...any) {
	if !c.options.Verbose || c.options.ErrOut == nil {
		return
	}
	fmt.Fprintf(c.options.ErrOut, "[octo] "+format+"\n", args...) //nolint:errcheck // stderr verbose log
}

func buildURL(base, path string, query url.Values) (string, error) {
	base = strings.TrimRight(base, "/")
	path = "/" + strings.TrimLeft(path, "/")
	u, err := url.Parse(base + path)
	if err != nil {
		return "", fmt.Errorf("build url: %w", err)
	}
	if len(query) > 0 {
		q := u.Query()
		for k, vs := range query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// --- retry logic ---

// retryableErr wraps an *output.ExitError plus an optional Retry-After.
// Used internally so doWithRetry can distinguish retryable responses from
// terminal ones without re-parsing the exit error.
type retryableErr struct {
	*output.ExitError
	retryAfter time.Duration
}

// Unwrap lets errors.As reach the embedded *ExitError so callers (e.g.
// output.AsExitError) can still get structured info after retries exhaust.
func (r *retryableErr) Unwrap() error {
	if r == nil {
		return nil
	}
	return r.ExitError
}

func isRetryable(err error) bool {
	var re *retryableErr
	return errors.As(err, &re)
}

func extractRetryAfterFromErr(err error) (time.Duration, bool) {
	var re *retryableErr
	if !errors.As(err, &re) {
		return 0, false
	}
	if re.retryAfter > 0 {
		return re.retryAfter, true
	}
	return 0, false
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// backoffDelay computes exponential backoff with full jitter. attempt is 1-based.
// Retry-After is NOT capped by maxDelay (per design §6.2); that handling lives
// in doWithRetry.
func backoffDelay(attempt int) time.Duration {
	// Guard against overflow: if the shift would exceed maxDelay, clamp early.
	// With defaultBaseDelay=500ms the shift overflows time.Duration around
	// attempt=34, but maxRetries=3 makes this a defensive check.
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	exp := defaultBaseDelay
	if shift < 63 {
		exp = defaultBaseDelay << shift // 500ms, 1s, 2s, 4s, ...
	}
	if exp <= 0 || exp > defaultMaxDelay {
		exp = defaultMaxDelay
	}
	jitter := jitterFraction() * float64(exp)
	return time.Duration(jitter)
}

// jitterFraction returns a value in [0.5, 1.0) to avoid thundering-herd
// collapse while still making meaningful progress. Uses crypto/rand — the
// calls are rare enough (≤ 3 retries) that cost is negligible.
func jitterFraction() float64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0.75 // deterministic fallback
	}
	u := binary.BigEndian.Uint64(b[:])
	f := float64(u>>11) / (1 << 53) // uniform [0,1)
	return 0.5 + 0.5*f
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}
