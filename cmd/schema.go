package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// newSchemaCmd returns `octo schema`. Two modes:
//   - `octo schema <operation-id>`: print the full schema for one operation.
//   - `octo schema --list [domain]`: enumerate operations; optionally filter
//     by service name.
//
// Output is a success envelope so agents can jq over it the same way as any
// other command.
func newSchemaCmd(f *cmdutil.Factory) *cobra.Command {
	var list bool

	cmd := &cobra.Command{
		Use:   "schema [operation-id | domain]",
		Short: "Inspect operation parameters and response schema from the registry",
		Long: `Print the request parameters, request body, and response schema for an
operation (e.g. "octo schema matter.create"), or list available operations
with --list.

Examples:
  octo schema --list                # all services and operations
  octo schema --list matter         # only matter operations
  octo schema matter.create         # full schema for one operation`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg := f.Registry()
			if reg == nil {
				ee := output.ErrWithHint("internal", "REGISTRY_UNAVAILABLE", "registry not initialized", "report as a bug")
				_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
				return ee
			}

			if list {
				return runSchemaList(f, args)
			}

			if len(args) == 0 {
				ee := output.ErrValidation("operation-id is required (or pass --list)", "try `octo schema --list` to see available operations")
				_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
				return ee
			}

			return runSchemaGet(f, args[0])
		},
	}

	cmd.Flags().BoolVar(&list, "list", false, "list available operations (optionally filtered by domain)")
	return cmd
}

func runSchemaList(f *cmdutil.Factory, args []string) error {
	reg := f.Registry()
	payload := map[string]any{
		"services": reg.ListServices(),
	}
	if len(args) == 1 {
		svc := args[0]
		ops := reg.ListOperations(svc)
		if ops == nil && reg.GetSpec(svc) == nil {
			ee := output.ErrValidation(
				fmt.Sprintf("unknown service %q", svc),
				fmt.Sprintf("known services: %v", reg.ListServices()),
			)
			_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
			return ee
		}
		payload["service"] = svc
		payload["operations"] = ops
	} else {
		payload["operations"] = reg.ListAllOperations()
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return f.EmitSuccess(buf)
}

func runSchemaGet(f *cmdutil.Factory, operationID string) error {
	reg := f.Registry()
	op, ok := reg.GetOperation(operationID)
	if !ok {
		ee := output.ErrValidation(
			fmt.Sprintf("unknown operation %q", operationID),
			"try `octo schema --list` to see available operations",
		)
		_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
		return ee
	}
	buf, err := json.Marshal(op)
	if err != nil {
		return err
	}
	return f.EmitSuccess(buf)
}
