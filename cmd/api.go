package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// newAPICmd implements the generic passthrough:
//
//	octo-cli api <METHOD> <PATH> [--params '{...}'] [--data '{...}']
//
// Auth, service URL routing, retry, envelope, and universal flags are reused
// verbatim. No spec consultation, no flag auto-generation, no pagination
// helper — this is the escape hatch when an endpoint isn't in the registry
// yet or when a caller needs to exercise something bespoke.
func newAPICmd(f *cmdutil.Factory) *cobra.Command {
	var paramsSpec, dataSpec, service string

	cmd := &cobra.Command{
		Use:   "api <METHOD> <PATH>",
		Short: "Call an arbitrary Octo API endpoint (generic passthrough)",
		Long: `Generic REST passthrough that reuses credentials, retry, and envelope output.

Examples:
  octo-cli api GET /api/v1/matters --params '{"status":"open"}'
  octo-cli api POST /api/v1/matters --data '{"title":"test"}'
  octo-cli api GET /v1/bot/events --service dmworkim
  octo-cli api POST /api/v1/matters --data @body.json
  octo-cli api POST /api/v1/matters --data @-       # read from stdin

Unlike service commands, this command does NOT consult the registry, so
flags are not auto-generated and --page-all is not available.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			path := args[1]
			if !strings.HasPrefix(path, "/") {
				ee := output.ErrValidation("path must start with '/'", "e.g. /api/v1/matters")
				_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
				return ee
			}

			q, err := parseParamsJSON(paramsSpec)
			if err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}

			rawData, err := cmdutil.ParseInput(f, dataSpec)
			if err != nil {
				ee := output.ErrValidation(fmt.Sprintf("--data: %v", err), "pass inline JSON, @file, or @-")
				_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
				return ee
			}
			var body any
			if len(rawData) > 0 {
				// UseNumber, matching every generated command: a plain decode turns each
				// JSON number into float64, which silently rounds a uint64 id above 2^53
				// — and a rounded id is a *valid* id pointing at a row the caller did not
				// name. This command is the escape hatch the enum guard points people at,
				// so it must not be the one place the lossless contract does not hold.
				dec := json.NewDecoder(bytes.NewReader(rawData))
				dec.UseNumber()
				if err := decodeStrict(dec, &body); err != nil {
					ee := output.ErrValidation(fmt.Sprintf("--data is not valid JSON: %v", err), "pass a JSON value or use @file/@-")
					_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
					return ee
				}
			}

			secrets, serr := apiSecretsForRequest(f.Registry(), method, path, body)
			if serr != nil {
				_ = f.EmitError(serr) //nolint:errcheck // best-effort emit before returning err
				return serr
			}

			cli, err := f.Client()
			if err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			respBody, err := cli.Do(cobraCmd.Context(), &client.Request{
				Service: service,
				Method:  method,
				Path:    path,
				Query:   q,
				Body:    body,
				// Recovered from the registry rather than from a command definition,
				// because this command's path is whatever the caller typed.
				SecretValues: secrets,
			})
			if err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			return f.EmitSuccess(respBody)
		},
	}

	cmd.Flags().StringVar(&paramsSpec, "params", "", "query parameters as JSON object (e.g. '{\"status\":\"open\"}')")
	cmd.Flags().StringVar(&dataSpec, "data", "", "request body: inline JSON, @filepath, or @- for stdin")
	cmd.Flags().StringVar(&service, "service", "", "service key override (matters | dmworkim). Default: OCTO_API_BASE_URL")
	return cmd
}

// apiSecretsForPath recovers the values `api` must treat as secrets, by matching the
// concrete path against the registry's path templates.
//
// `api` takes an arbitrary METHOD and PATH, so there is no command definition to read
// x-octo-secret off — which is why this path had no SecretValues at all, and why the
// masking boundary inside client.Do was a no-op here: redactSecrets short-circuits on an
// empty list. The registry is still the right source of truth, just reached by matching
// rather than by binding: a template and a concrete path agree when they have the same
// number of segments and every literal segment is equal, and each {placeholder} then
// names the parameter whose value sits in that position.
//
// Only parameters the spec marks secret are declared. Over-declaring would mask ordinary
// ids — file ids, space ids — out of every diagnostic, which is its own kind of damage.
//
// This matters for this branch specifically: drive.json introduces the first
// x-octo-secret *path* parameters in any embedded spec, so before it there was no
// credential-equivalent path value here to leak. The enum guard also tells callers that
// when a vocabulary is too narrow there is no way through except `octo-cli api`, so the
// CLI actively points people at this command.
// decodeStrict decodes exactly one JSON value and refuses trailing content.
//
// json.Unmarshal rejects trailing bytes for free; a json.Decoder stops after the first
// value, so switching to a Decoder for UseNumber silently accepted `{"a":1}{"b":2}` and
// `{"a":1}]`, truncating to the first value. That is the trap resolveBody documents, and
// this is the second time it has been walked into — hence one helper both callers use
// rather than the check written out twice.
//
// The check is a second Decode requiring io.EOF, not dec.More(): More reports whether
// another element follows *inside the current array or object*, so at top level it answers
// false for a stray "]" and lets it through.
func decodeStrict(dec *json.Decoder, into any) error {
	if err := dec.Decode(into); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected content after the first JSON value")
	}
	return nil
}

// apiSecretsForRequest recovers every value this request carries that the spec marks
// secret — path parameters and request-body leaves alike.
//
// The path half was written first and stopped there, which was a partial enumeration of the
// same kind this branch keeps producing: a spec can mark a secret in three places, and
// drive.json marks `password` in the *body* of share.access and share.download. Since every
// redaction keys off SecretValues and short-circuits when it is empty, missing the body meant
// --dry-run and --verbose printed the password verbatim.
//
// A non-string value at a secret property is refused rather than declared. See
// bodySecretValues for why refusing is the right half of that fix to take.
func apiSecretsForRequest(reg *registry.Registry, method, path string, body any) ([]string, *output.ExitError) {
	secrets := apiSecretsForPath(reg, method, path)
	if body == nil || reg == nil {
		return secrets, nil
	}
	if d := matchOperation(reg, method, path); d != nil && d.RequestBody != nil {
		found, err := bodySecretValues(d.RequestBody, body, "")
		if err != nil {
			return nil, output.ErrValidation(fmt.Sprintf("--data: %v", err),
				"quote the value — a declared secret is masked by string rules, and an unquoted one would print in full")
		}
		secrets = append(secrets, found...)
	}
	return secrets, nil
}

// matchOperation returns the operation whose path template matches, or nil.
func matchOperation(reg *registry.Registry, method, path string) *registry.OperationDetail {
	want := pathSegments(path)
	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok || !strings.EqualFold(d.Method, method) {
				continue
			}
			for _, template := range candidatePaths(d) {
				if _, matched := matchPathTemplate(template, want); matched {
					return d
				}
			}
		}
	}
	return nil
}

// bodySecretValues walks the supplied body against the schema and returns the values sitting
// at properties the spec marks secret, at any depth. A non-string value at such a property is
// an error rather than a collected secret.
//
// Refusing is the whole fix on this side, and it is deliberately not symmetrical with the
// response side. Both halves of the redaction path used to assume "declared secret ⇒ Go
// string": collection asserted child.(string), so a --data number was never declared, and
// redactBodyValue returned a json.Number verbatim, so declaring it would not have helped
// either. `api` does no schema validation, so an unquoted numeric password was accepted, sent
// and printed twice — in the request trace and in the error detail — and an all-digit share
// password is an ordinary choice with the quotes the only thing protecting it.
//
// Teaching both halves to stringify would fix it too, and would be worse here: it widens the
// masker over every numeric value in a request body, which masks ordinary ids out of every
// diagnostic. A type error costs the caller one retry with quotes and cannot disclose
// anything. The response side gets the masking treatment instead, because there the value
// arrives from the backend and no caller mistake is involved.
//
// The error names the property and never the value: this is the default-deny family, and the
// point of refusing is that the value has no masked spelling yet.
//
// The object and array walks are separate functions only to keep each under the complexity
// limit; the recursion is one traversal.
func bodySecretValues(schema *registry.SchemaInfo, value any, at string) ([]string, error) {
	switch v := value.(type) {
	case map[string]any:
		return objectSecretValues(schema, v, at)
	case []any:
		return arraySecretValues(schema, v, at)
	}
	return nil, nil
}

// objectSecretValues collects the secret leaves of one JSON object against its schema.
func objectSecretValues(schema *registry.SchemaInfo, obj map[string]any, at string) ([]string, error) {
	var out []string
	for name := range schema.Properties {
		prop := schema.Properties[name]
		child, present := obj[name]
		if !present {
			continue
		}
		where := joinPropertyPath(at, name)
		if prop.Secret {
			str, err := secretStringAt(where, child)
			if err != nil {
				return nil, err
			}
			if str != "" {
				out = append(out, str)
			}
			continue
		}
		if prop.Type == "object" || prop.Items != nil {
			nested, err := bodySecretValues(&prop, child, where)
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
		}
	}
	return out, nil
}

// arraySecretValues collects the secret leaves of one JSON array against its item schema.
func arraySecretValues(schema *registry.SchemaInfo, items []any, at string) ([]string, error) {
	if schema.Items == nil {
		return nil, nil
	}
	var out []string
	for i, item := range items {
		nested, err := bodySecretValues(schema.Items, item, fmt.Sprintf("%s[%d]", at, i))
		if err != nil {
			return nil, err
		}
		out = append(out, nested...)
	}
	return out, nil
}

// secretStringAt reads the value at a declared-secret property, refusing any JSON kind but a
// string. An empty string is not a secret to mask and is not an error either.
//
// An explicit null is left alone rather than refused: it carries no value to disclose, and
// `api` does no schema validation otherwise, so refusing it would reject a call the backend
// may well accept.
func secretStringAt(where string, child any) (string, error) {
	if child == nil {
		return "", nil
	}
	str, ok := child.(string)
	if !ok {
		return "", fmt.Errorf("%s is declared secret and must be a JSON string, not %s",
			where, jsonKindOf(child))
	}
	return str, nil
}

// joinPropertyPath names a body property for an error message.
func joinPropertyPath(at, name string) string {
	if at == "" {
		return name
	}
	return at + "." + name
}

// jsonKindOf names the JSON kind of a decoded value, for a type error that must not print
// the value itself.
func jsonKindOf(v any) string {
	switch v.(type) {
	case json.Number, int, int64, float64:
		return "a number"
	case bool:
		return "a boolean"
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	}
	return "a non-string value"
}

// pathSegments splits a concrete request path, dropping any query string or fragment.
func pathSegments(path string) []string {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	return strings.Split(strings.Trim(path, "/"), "/")
}

func apiSecretsForPath(reg *registry.Registry, method, path string) []string {
	if reg == nil {
		return nil
	}
	// pathSegments drops the query string and fragment. The PATH argument is whatever the
	// caller typed, so it can carry both — and leaving them on made the last segment
	// "access?x=1", which matches no template, so nothing was declared and the token
	// printed in full.
	want := pathSegments(path)
	var secrets []string
	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok || !strings.EqualFold(d.Method, method) {
				continue
			}
			for _, template := range candidatePaths(d) {
				values, matched := matchPathTemplate(template, want)
				if !matched {
					continue
				}
				for name, value := range values {
					if findSecretPathParam(d, name) && value != "" {
						secrets = append(secrets, value)
					}
				}
				break
			}
		}
	}
	return secrets
}

// candidatePaths returns every absolute path an operation can be reached at.
//
// A spec declares one path, but x-octo-mount-by-token-kind routes the same operation to a
// different prefix per credential kind — /v1/user/drive for a user key, /v1/bot/drive for a
// bot. Matching only the declared spelling would leave the other mount unmasked, and a
// caller using `api` against the mount their token actually needs is the likely case, not
// the unlikely one.
func candidatePaths(d *registry.OperationDetail) []string {
	paths := []string{d.Path}
	for _, from := range d.MountByTokenKind {
		suffix, ok := strings.CutPrefix(d.Path, from)
		if !ok {
			continue
		}
		for _, to := range d.MountByTokenKind {
			if to == from {
				continue
			}
			paths = append(paths, to+suffix)
		}
		break
	}
	return paths
}

// matchPathTemplate reports whether a "/a/{b}/c" template matches the given concrete
// segments, and returns the value each placeholder captured.
func matchPathTemplate(template string, got []string) (map[string]string, bool) {
	want := strings.Split(strings.Trim(template, "/"), "/")
	if len(want) != len(got) {
		return nil, false
	}
	values := map[string]string{}
	for i, seg := range want {
		if name, isParam := strings.CutPrefix(seg, "{"); isParam {
			name = strings.TrimSuffix(name, "}")
			values[name] = got[i]
			continue
		}
		if seg != got[i] {
			return nil, false
		}
	}
	return values, true
}

// findSecretPathParam reports whether the operation declares name as a secret path
// parameter.
func findSecretPathParam(d *registry.OperationDetail, name string) bool {
	for i := range d.Parameters {
		p := &d.Parameters[i]
		if p.In == "path" && p.Name == name {
			return p.Secret
		}
	}
	return false
}

// parseParamsJSON turns --params '{"k":"v","n":1,"arr":["a","b"]}' into
// url.Values. Scalars are stringified; arrays expand into repeat keys. Nested
// objects are marshalled back to JSON strings — callers who need that can
// still use them, but the shape is discouraged.
func parseParamsJSON(spec string) (url.Values, error) {
	if spec == "" {
		return nil, nil
	}
	var obj map[string]any
	dec := json.NewDecoder(strings.NewReader(spec))
	dec.UseNumber()
	if err := decodeStrict(dec, &obj); err != nil {
		return nil, output.ErrValidation(
			fmt.Sprintf("--params is not a JSON object: %v", err),
			"pass an object like '{\"status\":\"open\"}'",
		)
	}
	// null decodes into a map without failing and leaves obj nil. Ranging a nil map is
	// a no-op, so this used to mean "no query parameters" rather than being refused —
	// the same unchecked-successful-decode as --data null, with a quieter symptom.
	if obj == nil {
		return nil, output.ErrValidation(
			"--params is not a JSON object: null",
			"pass an object like '{\"status\":\"open\"}', or omit --params",
		)
	}
	q := url.Values{}
	for k, v := range obj {
		if v == nil {
			// A top-level null means "no such parameter": omitting it is what a caller
			// merging JSON from a template expects, and there is no arity to preserve.
			continue
		}
		if items, ok := v.([]any); ok {
			for _, item := range items {
				q.Add(k, paramValue(item))
			}
			continue
		}
		q.Set(k, paramValue(v))
	}
	return q, nil
}

// paramValue renders one query value.
//
// Scalars keep their plain spelling — a number keeps the exact digits the caller typed,
// because decoding into float64 and formatting back through int64 rounded 9007199254740993
// to …92, and a rounded id is a *valid* id addressing a row nobody asked for. Anything a
// query string cannot carry natively is JSON-encoded.
//
// One function for both positions, because they used to disagree. The array arm fell through
// to fmt.Sprintf("%v", item), so an object element became `map[id:9007199254740993]`, a
// nested array `[1 2]` and null `<nil>` — Go's debug spelling on the wire — while the same
// value at top level was marshalled correctly. A null *element* is encoded as `null` rather
// than skipped, since dropping it would change the array's arity.
func paramValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	buf, err := json.Marshal(v)
	if err != nil {
		// Unreachable for a value decoded from JSON, and the safe answer if it ever is:
		// an empty value rather than a Go-syntax dump.
		return ""
	}
	return string(buf)
}
