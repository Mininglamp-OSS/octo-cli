package service

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestNumberTextSerializationUsesPlainDecimal(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "10000000", want: "10000000"},
		{input: "10000000.25", want: "10000000.25"},
		{input: "-10000000", want: "-10000000"},
		{input: "0.0000001", want: "0.0000001"},
		{input: "-0.0000001", want: "-0.0000001"},
		{input: "1e7", want: "10000000"},
		{input: "1e-7", want: "0.0000001"},
		{input: "4.5", want: "4.5"},
		{input: "0", want: "0"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Run("query", func(t *testing.T) {
				rt := &operationRuntime{
					detail: &registry.OperationDetail{Parameters: []registry.ParamInfo{
						{Name: "position", In: "query", Type: "number"},
					}},
					queryFlags: map[string]*queryFlag{},
				}
				cmd := &cobra.Command{Use: "probe"}
				registerQueryFlags(cmd, rt, rt.detail)
				if err := cmd.ParseFlags([]string{"--position", tc.input}); err != nil {
					t.Fatal(err)
				}
				query, err := buildQuery(cmd, rt)
				if err != nil {
					t.Fatal(err)
				}
				if got := query.Get("position"); got != tc.want {
					t.Errorf("query position = %q, want %q", got, tc.want)
				}
			})
			t.Run("multipart", func(t *testing.T) {
				cmd := &cobra.Command{Use: "probe"}
				value := cmd.Flags().Float64("position", 0, "")
				if err := cmd.ParseFlags([]string{"--position", tc.input}); err != nil {
					t.Fatal(err)
				}
				field := &bodyFlag{apiName: "position", kind: kindFloat64, floatVal: value}
				if got := multipartScalarValue(field); got != tc.want {
					t.Errorf("multipart position = %q, want %q", got, tc.want)
				}
			})
		})
	}
}
