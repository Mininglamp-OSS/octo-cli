// Command permission-catalog materializes pinned source content offline. Its
// output is deliberately not a supported permission release or allowlist.
package main

import (
	"fmt"
	"os"

	"github.com/Mininglamp-OSS/octo-cli/internal/mcp"
)

func main() {
	b, err := mcp.MaterializePermissionSources()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err = os.Stdout.Write(b); err != nil {
		os.Exit(1)
	}
}
