package cmd

import (
	"encoding/json"
	"fmt"
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
				if err := json.Unmarshal(rawData, &body); err != nil {
					ee := output.ErrValidation(fmt.Sprintf("--data is not valid JSON: %v", err), "pass a JSON value or use @file/@-")
					_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
					return ee
				}
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
				SecretValues: apiSecretsForPath(f.Registry(), method, path),
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
func apiSecretsForPath(reg *registry.Registry, method, path string) []string {
	if reg == nil {
		return nil
	}
	want := strings.Split(strings.Trim(path, "/"), "/")
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
	if err := json.Unmarshal([]byte(spec), &obj); err != nil {
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
		switch val := v.(type) {
		case nil:
			// skip
		case string:
			q.Set(k, val)
		case bool:
			if val {
				q.Set(k, "true")
			} else {
				q.Set(k, "false")
			}
		case float64:
			if val == float64(int64(val)) {
				q.Set(k, fmt.Sprintf("%d", int64(val)))
			} else {
				q.Set(k, fmt.Sprintf("%g", val))
			}
		case []any:
			for _, item := range val {
				q.Add(k, fmt.Sprintf("%v", item))
			}
		default:
			buf, _ := json.Marshal(val) //nolint:errcheck // val is pre-validated JSON-safe type
			q.Set(k, string(buf))
		}
	}
	return q, nil
}
