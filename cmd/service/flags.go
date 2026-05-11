package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dmwork-org/octo-cli/internal/registry"
)

// operationRuntime holds everything the RunE closure needs to build the
// outbound request from the user's flag values.
type operationRuntime struct {
	detail     *registry.OperationDetail
	pathParams []string              // path param names in order
	queryFlags map[string]*queryFlag // flag name → query param binding
	bodyFlags  map[string]*bodyFlag  // flag name → body field binding
	bodyData   *string               // --data (nil when command has no body)
	pageAll    *bool                 // --page-all (nil when no pagination)
	pageLimit  *int                  // --page-limit
	filePath   *string               // --file (multipart operations only)
}

type queryFlag struct {
	apiName string // URL query parameter name
	kind    valueKind
	strVal  *string
	intVal  *int
	boolVal *bool
	strSlc  *[]string
}

type bodyFlag struct {
	apiName string // JSON body field name
	kind    valueKind
	strVal  *string
	intVal  *int
	boolVal *bool
	strSlc  *[]string
}

type valueKind int

const (
	kindString valueKind = iota
	kindInt
	kindBool
	kindStringSlice
)

func registerQueryFlags(cmd *cobra.Command, rt *operationRuntime, d *registry.OperationDetail) {
	for _, p := range d.Parameters {
		if p.In != "query" {
			continue
		}
		flagName := strings.ReplaceAll(p.Name, "_", "-")
		qf := &queryFlag{apiName: p.Name, kind: schemaTypeKind(p.Type)}
		desc := p.Description
		if len(p.Enum) > 0 {
			desc = fmt.Sprintf("%s (one of: %s)", desc, formatEnum(p.Enum))
		}
		switch qf.kind {
		case kindInt:
			qf.intVal = new(int)
			dv := 0
			if n, ok := p.Default.(float64); ok {
				dv = int(n)
			}
			cmd.Flags().IntVar(qf.intVal, flagName, dv, desc)
		case kindBool:
			qf.boolVal = new(bool)
			dv := false
			if b, ok := p.Default.(bool); ok {
				dv = b
			}
			cmd.Flags().BoolVar(qf.boolVal, flagName, dv, desc)
		case kindStringSlice:
			qf.strSlc = new([]string)
			cmd.Flags().StringSliceVar(qf.strSlc, flagName, nil, desc)
		default:
			qf.strVal = new(string)
			dv := ""
			if s, ok := p.Default.(string); ok {
				dv = s
			}
			cmd.Flags().StringVar(qf.strVal, flagName, dv, desc)
		}
		rt.queryFlags[flagName] = qf
		if p.Required {
			_ = cmd.MarkFlagRequired(flagName)
		}
	}
}

func registerBodyFlags(cmd *cobra.Command, rt *operationRuntime, d *registry.OperationDetail) {
	body := d.RequestBody
	if body == nil {
		return
	}

	// Multipart operations: register --file for the binary upload and skip
	// --data (JSON body doesn't apply). Non-binary body fields still register
	// as flags below so they can go through as form text fields.
	if d.Multipart {
		filePath := new(string)
		cmd.Flags().StringVar(filePath, "file", "", "path to the file to upload (required)")
		_ = cmd.MarkFlagRequired("file")
		rt.filePath = filePath
	} else {
		// Every non-multipart command with a body gets --data, even when we
		// also promote simple fields. Individual flags override the JSON blob
		// (architecture §5.2).
		data := new(string)
		cmd.Flags().StringVar(data, "data", "", "JSON request body (string, @file, or @- for stdin). Individual flags override.")
		rt.bodyData = data
	}

	if body.Properties == nil {
		return
	}
	required := map[string]bool{}
	for _, r := range body.Required {
		required[r] = true
	}
	// Deterministic registration order for predictable --help.
	names := make([]string, 0, len(body.Properties))
	for k := range body.Properties {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		prop := body.Properties[name]
		// Skip binary fields — those are handled by --file in multipart mode.
		if prop.Format == "binary" {
			continue
		}
		kind, ok := promotableKind(prop)
		if !ok {
			continue
		}
		flagName := strings.ReplaceAll(name, "_", "-")
		// Avoid collisions with --data, --file or a query param of the same name.
		if flagName == "data" || flagName == "file" || rt.queryFlags[flagName] != nil {
			continue
		}
		desc := prop.Description
		if len(prop.Enum) > 0 {
			desc = fmt.Sprintf("%s (one of: %s)", desc, formatEnum(prop.Enum))
		}
		if required[name] {
			desc = strings.TrimSpace(desc + " (required)")
		}
		bf := &bodyFlag{apiName: name, kind: kind}
		switch kind {
		case kindInt:
			bf.intVal = new(int)
			cmd.Flags().IntVar(bf.intVal, flagName, 0, desc)
		case kindBool:
			bf.boolVal = new(bool)
			cmd.Flags().BoolVar(bf.boolVal, flagName, false, desc)
		case kindStringSlice:
			bf.strSlc = new([]string)
			cmd.Flags().StringSliceVar(bf.strSlc, flagName, nil, desc)
		default:
			bf.strVal = new(string)
			cmd.Flags().StringVar(bf.strVal, flagName, "", desc)
		}
		rt.bodyFlags[flagName] = bf
	}
}

// promotableKind returns the primitive flag kind a body property maps to,
// or (0,false) if the property is complex (object, array-of-object, etc.)
// and must go through --data. Enums inherit their base type.
func promotableKind(p registry.SchemaInfo) (valueKind, bool) {
	switch p.Type {
	case "string":
		return kindString, true
	case "integer", "number":
		return kindInt, true
	case "boolean":
		return kindBool, true
	case "array":
		if p.Items != nil && p.Items.Type == "string" {
			return kindStringSlice, true
		}
	}
	return 0, false
}

func schemaTypeKind(t string) valueKind {
	switch t {
	case "integer", "number":
		return kindInt
	case "boolean":
		return kindBool
	}
	return kindString
}

func formatEnum(values []any) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return strings.Join(parts, ", ")
}
