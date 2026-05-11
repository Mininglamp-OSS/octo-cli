package service

import (
	"net/url"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// attachMatterStatusAliases registers `octo matter close/reopen/archive <id>`
// as fixed-payload wrappers over the matter.transition operation. Status
// transitions have no state machine (architecture §8.1): any → any is a
// valid move, so each alias just hardcodes the target status value.
func attachMatterStatusAliases(svcCmd *cobra.Command, f *cmdutil.Factory) {
	for _, a := range []struct {
		name, short, status string
	}{
		{"close", "Close a matter (sets status=done)", "done"},
		{"reopen", "Reopen a matter (sets status=open)", "open"},
		{"archive", "Archive a matter (sets status=archived)", "archived"},
	} {
		svcCmd.AddCommand(newMatterStatusAlias(f, a.name, a.short, a.status))
	}
}

func newMatterStatusAlias(f *cmdutil.Factory, name, short, target string) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := client.Request{
				Service: "matters",
				Method:  "PUT",
				Path:    "/api/v1/matters/" + url.PathEscape(args[0]) + "/status",
				Body:    map[string]string{"status": target},
			}
			return emitOnce(cmd.Context(), f, &req)
		},
	}
}
