package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dmwork-org/octo-cli/internal/client"
	"github.com/dmwork-org/octo-cli/internal/config"
)

var (
	formatFlag string
	cfg        *config.Config
	apiClient  *client.Client
)

// version is set at build time via -ldflags.
var version = "dev"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "octo",
		Short: "Octo CLI — command-line interface for the Octo ecosystem",
		Long:  "octo is a CLI for AI Agent Bots to interact with Octo services.\nCurrent module: octo todo",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip auth validation for version command
			if cmd.Name() == "version" {
				return nil
			}
			cfg = config.Load()
			if formatFlag != "" {
				cfg.Format = formatFlag
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			var clientErr error
			apiClient, clientErr = client.New(cfg)
			return clientErr
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&formatFlag, "format", "", "output format: json (default) or table")

	root.AddCommand(newTodoCmd())
	root.AddCommand(newVersionCmd())

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print octo-cli version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(os.Stdout, "octo version %s\n", version)
		},
	}
}
