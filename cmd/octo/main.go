package main

import (
	"fmt"
	"os"

	"github.com/dmwork-org/octo-cli/internal/cli"
	"github.com/dmwork-org/octo-cli/internal/output"
)

func main() {
	cmd := cli.NewRootCmd()
	if err := cmd.Execute(); err != nil {
		output.PrintError(err)
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}
}
