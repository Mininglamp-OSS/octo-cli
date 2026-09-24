package service

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
	"github.com/spf13/cobra"
)

func TestDeclaredBooleanBodyDefaults(test *testing.T) {
	for _, scenario := range []struct {
		name     string
		declared any
		data     any
		flag     string
		want     any
	}{
		{name: "absent declaration stays absent"},
		{name: "declared true", declared: true, want: true},
		{name: "declared false", declared: false, want: false},
		{name: "data false preserved", declared: true, data: false, want: false},
		{name: "data true preserved", declared: false, data: true, want: true},
		{name: "flag false wins", declared: true, data: true, flag: "--enabled=false", want: false},
		{name: "flag true wins", declared: false, data: false, flag: "--enabled", want: true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			command := &cobra.Command{Use: "test"}
			runtime := &operationRuntime{bodyFlags: map[string]*bodyFlag{}}
			detail := &registry.OperationDetail{RequestBody: &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{
				"enabled":   {Type: "boolean", Default: scenario.declared},
				"untouched": {Type: "boolean"},
			}}}
			registerBodyFlags(command, runtime, detail)
			if scenario.declared == true && command.Flags().Lookup("enabled").DefValue != "true" {
				test.Fatal("flag help lost declared true default")
			}
			if scenario.flag != "" {
				if err := command.ParseFlags([]string{scenario.flag}); err != nil {
					test.Fatal(err)
				}
			}
			body := map[string]any{}
			if scenario.data != nil {
				body["enabled"] = scenario.data
			}
			if err := applyBodyFlags(command, runtime, body); err != nil {
				test.Fatal(err)
			}
			if body["enabled"] != scenario.want {
				test.Fatalf("body=%#v; want enabled=%#v", body, scenario.want)
			}
			if _, exists := body["untouched"]; exists {
				test.Fatal("undeclared default must remain absent")
			}
		})
	}
}

func TestStrictBooleanValidationPreservesLegacyClears(test *testing.T) {
	for _, strict := range []bool{false, true} {
		validator := bodySchemaValidator{enforcePublicAPIConstraints: strict}
		for _, value := range []any{true, false, nil, "false", 0} {
			err := validator.validate(&registry.SchemaInfo{Type: "boolean"}, value, "enabled", "")
			_, boolean := value.(bool)
			if (err != nil) != (strict && !boolean) {
				test.Errorf("strict=%v, value=%#v: %v", strict, value, err)
			}
		}
		for _, kind := range []string{"object", "array", "string", "integer"} {
			if err := validator.validate(&registry.SchemaInfo{Type: kind}, nil, "clear", ""); err != nil {
				test.Errorf("existing %s null clear changed: %v", kind, err)
			}
		}
	}
}
