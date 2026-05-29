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
