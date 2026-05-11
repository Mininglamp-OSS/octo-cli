package cmd

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/dmwork-org/octo-cli/internal/cmdutil"
)

// newVersionCmd prints octo-cli version metadata.
func newVersionCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print octo-cli version",
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]any{
				"version":    Version,
				"commit":     Commit,
				"build_date": BuildDate,
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return f.EmitError(err)
			}
			return f.EmitSuccess(raw)
		},
	}
}
